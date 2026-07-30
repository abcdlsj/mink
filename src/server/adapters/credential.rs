use argon2::{Argon2, PasswordHash, PasswordHasher, PasswordVerifier, password_hash::SaltString};
use uuid::Uuid;

use crate::server::application::ports::{
    ApplicationError, PasswordPort, RawSessionToken, SessionTokenPort,
};

/// Argon2 密码散列。salt 每次注册重新生成。
pub(super) struct Argon2Passwords;

impl PasswordPort for Argon2Passwords {
    fn hash(&self, password: &str) -> Result<String, ApplicationError> {
        let salt = SaltString::encode_b64(Uuid::now_v7().as_bytes())
            .map_err(|_| ApplicationError::Internal)?;
        Ok(Argon2::default()
            .hash_password(password.as_bytes(), &salt)
            .map_err(|_| ApplicationError::Internal)?
            .to_string())
    }

    fn verify(&self, password: &str, stored_hash: &str) -> bool {
        // 散列损坏与密码不符都返回 false：调用方不得据此区分账号是否存在。
        PasswordHash::new(stored_hash).is_ok_and(|parsed| {
            Argon2::default()
                .verify_password(password.as_bytes(), &parsed)
                .is_ok()
        })
    }
}

/// Browser Session token。两个 UUIDv7 拼接提供 256 位随机源。
pub(super) struct UuidSessionTokens;

impl SessionTokenPort for UuidSessionTokens {
    fn generate(&self) -> RawSessionToken {
        RawSessionToken::new(format!(
            "{}{}",
            Uuid::now_v7().simple(),
            Uuid::now_v7().simple()
        ))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn password_verification_rejects_wrong_password_and_corrupt_hash() {
        let passwords = Argon2Passwords;
        let hash = passwords.hash("correct horse battery").expect("hash");
        assert!(passwords.verify("correct horse battery", &hash));
        assert!(!passwords.verify("wrong password", &hash));
        assert!(!passwords.verify("correct horse battery", "not-a-hash"));
    }

    #[test]
    fn session_tokens_are_unique_and_hidden_from_debug_output() {
        let tokens = UuidSessionTokens;
        let first = tokens.generate();
        let second = tokens.generate();
        assert_ne!(first.expose(), second.expose());
        assert_ne!(first.sha256_hash(), second.sha256_hash());
        let rendered = format!("{first:?}");
        assert!(!rendered.contains(first.expose()));
    }
}
