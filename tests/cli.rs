use assert_cmd::Command;
use std::io::{BufRead, Write};

#[test]
fn exposes_one_binary_with_three_primary_commands() {
    let help = Command::cargo_bin("sumi")
        .unwrap()
        .arg("--help")
        .output()
        .unwrap();
    let stdout = String::from_utf8(help.stdout).unwrap();

    assert!(help.status.success());
    assert!(stdout.contains("server"));
    assert!(stdout.contains("computer"));
    assert!(stdout.contains("agent"));
    assert!(!stdout.contains("chat"));
}

#[test]
fn agent_commands_reject_unscoped_processes() {
    Command::cargo_bin("sumi")
        .unwrap()
        .args(["agent", "whoami", "--json"])
        .env_remove("SUMI_SOCKET")
        .env_remove("SUMI_RUN_TOKEN")
        .assert()
        .failure();
    for args in [
        vec!["agent", "inbox", "current", "--json"],
        vec!["agent", "channel", "read", "#general", "--json"],
        vec!["agent", "task", "list", "--json"],
        vec![
            "agent", "message", "send", "#general", "--body", "hello", "--json",
        ],
        vec![
            "agent",
            "attachment",
            "info",
            "01969f98-bcee-7da0-a150-e0d0de169c00",
            "--json",
        ],
    ] {
        Command::cargo_bin("sumi")
            .unwrap()
            .args(args)
            .env_remove("SUMI_SOCKET")
            .env_remove("SUMI_RUN_TOKEN")
            .assert()
            .failure();
    }
}

#[test]
fn agent_cli_uses_stable_exit_codes_and_keeps_json_errors_structured() {
    let invalid_arguments = Command::cargo_bin("sumi")
        .unwrap()
        .args(["agent", "message", "send", "#general", "--json"])
        .env("SUMI_RUN_TOKEN", "run-token")
        .env("SUMI_SOCKET", "/definitely/missing/sumi.sock")
        .output()
        .unwrap();
    assert_eq!(invalid_arguments.status.code(), Some(2));
    assert_json_error(&invalid_arguments.stdout, "invalid_argument", false);

    let parse_error = Command::cargo_bin("sumi")
        .unwrap()
        .args(["agent", "inbox", "show", "not-a-uuid", "--json"])
        .output()
        .unwrap();
    assert_eq!(parse_error.status.code(), Some(2));
    assert_json_error(&parse_error.stdout, "invalid_argument", false);

    let missing_capability = Command::cargo_bin("sumi")
        .unwrap()
        .args(["agent", "context", "current", "--json"])
        .env_remove("SUMI_RUN_TOKEN")
        .env_remove("SUMI_SOCKET")
        .output()
        .unwrap();
    assert_eq!(missing_capability.status.code(), Some(1));
    assert_json_error(&missing_capability.stdout, "unauthenticated", false);

    let unavailable = Command::cargo_bin("sumi")
        .unwrap()
        .args(["agent", "context", "current", "--json"])
        .env("SUMI_RUN_TOKEN", "run-token")
        .env("SUMI_SOCKET", "/definitely/missing/sumi.sock")
        .output()
        .unwrap();
    assert_eq!(unavailable.status.code(), Some(1));
    assert_json_error(&unavailable.stdout, "unavailable", true);

    for (error_code, retryable) in [
        ("permission_denied", false),
        ("not_found", false),
        ("conflict", false),
        ("context_changed", false),
        ("rate_limited", true),
        ("unavailable", true),
        ("internal", false),
    ] {
        let root = tempfile::tempdir().unwrap();
        let socket = root.path().join("daemon.sock");
        let listener = std::os::unix::net::UnixListener::bind(&socket).unwrap();
        let response = serde_json::json!({
            "schema_version": 1,
            "ok": false,
            "data": null,
            "error": {
                "code": error_code,
                "message": "classified failure",
                "retryable": retryable,
                "details": if error_code == "context_changed" {
                    serde_json::json!({
                        "snapshot_channel_seq": 10,
                        "latest_channel_seq": 11,
                        "changes": [{
                            "id": "01969f98-bcee-7da0-a150-e0d0de169c00",
                            "seq": 11,
                            "address": "#general:1",
                            "thread_id": 1,
                            "author": { "id": "01969f98-bcee-7da0-a150-e0d0de169c01" }
                        }],
                        "has_more": false
                    })
                } else {
                    serde_json::json!({})
                }
            }
        });
        let server = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = String::new();
            std::io::BufReader::new(stream.try_clone().unwrap())
                .read_line(&mut request)
                .unwrap();
            assert!(request.contains("\"run_token\":\"run-token\""));
            writeln!(stream, "{response}").unwrap();
        });
        let output = Command::cargo_bin("sumi")
            .unwrap()
            .args(["agent", "context", "current", "--json"])
            .env("SUMI_RUN_TOKEN", "run-token")
            .env("SUMI_SOCKET", &socket)
            .output()
            .unwrap();
        server.join().unwrap();
        assert_eq!(output.status.code(), Some(1));
        let envelope: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(envelope["error"]["code"], error_code);
        if error_code == "context_changed" {
            assert_eq!(envelope["error"]["details"]["latest_channel_seq"], 11);
            assert!(
                envelope["error"]["details"]["changes"][0]
                    .get("body_markdown")
                    .is_none()
            );
        }
    }
}

fn assert_json_error(stdout: &[u8], code: &str, retryable: bool) {
    let envelope: serde_json::Value = serde_json::from_slice(stdout).unwrap();
    assert_eq!(envelope["schema_version"], 1);
    assert_eq!(envelope["ok"], false);
    assert!(envelope["data"].is_null());
    assert_eq!(envelope["error"]["code"], code);
    assert_eq!(envelope["error"]["retryable"], retryable);
}
