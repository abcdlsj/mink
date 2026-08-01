use std::{fs, path::Path};

#[test]
fn new_modules_do_not_cross_forbidden_dependency_boundaries() {
    assert_forbidden(
        "src/protocol",
        &["crate::server", "crate::computer", "crate::driver"],
    );
    assert_forbidden(
        "src/server/domain",
        &["crate::protocol", "crate::computer", "crate::driver"],
    );
    assert_forbidden(
        "src/server/application",
        &["crate::computer", "crate::driver"],
    );
    assert_forbidden(
        "src/computer/core",
        &["crate::protocol", "crate::server", "crate::driver"],
    );
    assert_forbidden("src/computer/application", &["crate::server"]);
    assert_forbidden(
        "src/computer/adapters",
        &[
            "crate::computer::core",
            "computer::core",
            "core::{",
            "crate::server",
            "crate::driver",
        ],
    );
    assert_forbidden("src/computer/drivers", &["crate::server", "sqlx::postgres"]);
    assert_forbidden(
        "src/agent_cli",
        &["crate::server", "crate::computer::core", "sqlx"],
    );
    assert_scoped_visibility("src/ids.rs");
    assert_scoped_visibility("src/config.rs");
    assert_scoped_visibility("src/cli.rs");
    assert_scoped_visibility("src/protocol");
    assert_scoped_visibility("src/server");
    assert_scoped_visibility("src/computer");
    assert_scoped_visibility("src/agent_cli");
    assert_http_handlers_do_not_own_write_transactions();
    assert_websocket_does_not_apply_agent_lifecycle();
}

#[test]
fn persisted_entity_rehydrate_is_confined_to_postgres_rows() {
    assert_rehydrate_calls_are_confined_to_postgres_rows();
}

fn assert_rehydrate_calls_are_confined_to_postgres_rows() {
    let manifest = Path::new(env!("CARGO_MANIFEST_DIR"));
    let root = manifest.join("src/server");
    visit_rust_files(&root, &mut |path, source| {
        let relative = path
            .strip_prefix(manifest)
            .expect("server source must be inside the manifest directory");
        if relative.starts_with("src/server/domain")
            || relative == Path::new("src/server/adapters/postgres/rows.rs")
            || path.file_name().is_some_and(|name| name == "tests.rs")
        {
            return;
        }
        assert!(
            !source.contains("::rehydrate("),
            "{} calls a persistence-only rehydrate constructor",
            path.display()
        );
    });
}

fn assert_http_handlers_do_not_own_write_transactions() {
    let manifest = Path::new(env!("CARGO_MANIFEST_DIR"));
    let root = manifest.join("src/server/adapters/http");
    visit_rust_files(&root, &mut |path, source| {
        if path.file_name().is_some_and(|name| name == "tests.rs") {
            return;
        }
        let source = source.to_ascii_lowercase();
        for forbidden in [
            "\"insert into ",
            "\"update ",
            "\"delete from ",
            "pool.begin(",
        ] {
            assert!(
                !source.contains(forbidden),
                "{} contains HTTP-owned write transaction marker {forbidden}",
                path.display()
            );
        }
    });
}

fn assert_websocket_does_not_apply_agent_lifecycle() {
    let path = Path::new(env!("CARGO_MANIFEST_DIR")).join("src/server/adapters/websocket.rs");
    let source = fs::read_to_string(&path)
        .unwrap_or_else(|error| panic!("failed to read {}: {error}", path.display()))
        .to_ascii_lowercase();
    assert!(
        !source.contains("update agents"),
        "WebSocket adapter must delegate Agent lifecycle results to application"
    );
}

fn assert_forbidden(relative_root: &str, forbidden: &[&str]) {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join(relative_root);
    visit_rust_files(&root, &mut |path, source| {
        for dependency in forbidden {
            assert!(
                !source.contains(dependency),
                "{} contains forbidden dependency {dependency}",
                path.display()
            );
        }
    });
}

fn assert_scoped_visibility(relative_root: &str) {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join(relative_root);
    if root.is_file() {
        let source = fs::read_to_string(&root)
            .unwrap_or_else(|error| panic!("failed to read {}: {error}", root.display()));
        assert_source_has_scoped_visibility(&root, &source);
        return;
    }
    visit_rust_files(&root, &mut assert_source_has_scoped_visibility);
}

fn assert_source_has_scoped_visibility(path: &Path, source: &str) {
    for (index, line) in source.lines().enumerate() {
        assert!(
            !line.trim_start().starts_with("pub "),
            "{}:{} uses unscoped public visibility",
            path.display(),
            index + 1
        );
    }
}

fn visit_rust_files(root: &Path, visitor: &mut impl FnMut(&Path, &str)) {
    for entry in fs::read_dir(root).unwrap_or_else(|error| {
        panic!(
            "failed to read architecture root {}: {error}",
            root.display()
        )
    }) {
        let path = entry.expect("failed to read architecture entry").path();
        if path.is_dir() {
            visit_rust_files(&path, visitor);
        } else if path.extension().is_some_and(|extension| extension == "rs") {
            let source = fs::read_to_string(&path)
                .unwrap_or_else(|error| panic!("failed to read {}: {error}", path.display()));
            visitor(&path, &source);
        }
    }
}
