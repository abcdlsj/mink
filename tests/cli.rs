use assert_cmd::Command;

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
