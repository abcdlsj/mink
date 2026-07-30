use argon2::{Argon2, PasswordHash, PasswordHasher, PasswordVerifier, password_hash::SaltString};
use uuid::Uuid;

use crate::server::application::ports::{
    ApplicationError, InvitationTokenPort, PairingCodePort, PasswordPort, RawInvitationToken,
    RawPairingCode, RawSessionToken, SessionTokenPort,
};

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
        // A malformed hash and a wrong password are intentionally indistinguishable.
        PasswordHash::new(stored_hash).is_ok_and(|parsed| {
            Argon2::default()
                .verify_password(password.as_bytes(), &parsed)
                .is_ok()
        })
    }
}

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

pub(super) struct UuidInvitationTokens;

impl InvitationTokenPort for UuidInvitationTokens {
    fn generate(&self) -> RawInvitationToken {
        RawInvitationToken::new(format!(
            "{}{}",
            Uuid::now_v7().simple(),
            Uuid::now_v7().simple()
        ))
    }
}

pub(super) struct NumericPairingCodes;

impl PairingCodePort for NumericPairingCodes {
    fn generate(&self) -> RawPairingCode {
        let bytes: [u8; 4] = Uuid::now_v7().as_bytes()[..4]
            .try_into()
            .expect("a UUID provides at least four bytes");
        RawPairingCode::new(format!("{:06}", u32::from_be_bytes(bytes) % 1_000_000))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pairing_codes_are_six_digits_and_hidden_from_debug_output() {
        let code = NumericPairingCodes.generate();
        assert_eq!(code.expose().len(), 6);
        assert!(code.expose().bytes().all(|byte| byte.is_ascii_digit()));
        assert!(!format!("{code:?}").contains(code.expose()));
    }

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

    #[test]
    fn invitation_tokens_are_unique_and_hidden_from_debug_output() {
        let tokens = UuidInvitationTokens;
        let first = tokens.generate();
        let second = tokens.generate();
        assert_ne!(first.expose(), second.expose());
        assert_ne!(first.sha256_hash(), second.sha256_hash());
        assert!(!format!("{first:?}").contains(first.expose()));
    }
}
