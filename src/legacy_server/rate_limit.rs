use std::num::NonZeroU32;

use governor::{DefaultKeyedRateLimiter, Quota, RateLimiter};

use super::api_error::ApiError;

pub struct AuthRateLimits {
    by_ip: DefaultKeyedRateLimiter<String>,
    by_email: DefaultKeyedRateLimiter<String>,
    pairing_by_ip: DefaultKeyedRateLimiter<String>,
}

impl AuthRateLimits {
    pub fn new(ip_attempts_per_minute: u32, email_attempts_per_minute: u32) -> Self {
        let ip_quota = Quota::per_minute(
            NonZeroU32::new(ip_attempts_per_minute)
                .expect("auth_ip_attempts_per_minute must be positive"),
        );
        let email_quota = Quota::per_minute(
            NonZeroU32::new(email_attempts_per_minute)
                .expect("auth_email_attempts_per_minute must be positive"),
        );
        Self {
            by_ip: RateLimiter::keyed(ip_quota),
            by_email: RateLimiter::keyed(email_quota),
            pairing_by_ip: RateLimiter::keyed(ip_quota),
        }
    }

    pub fn check_ip(&self, ip: String) -> Result<(), ApiError> {
        self.by_ip.check_key(&ip).map_err(|_| ApiError::RateLimited)
    }

    pub fn check_email(&self, email: &str) -> Result<(), ApiError> {
        self.by_email
            .check_key(&email.to_owned())
            .map_err(|_| ApiError::RateLimited)
    }

    pub fn check_pairing_ip(&self, ip: String) -> Result<(), ApiError> {
        self.pairing_by_ip
            .check_key(&ip)
            .map_err(|_| ApiError::RateLimited)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn limits_each_ip_and_email_key_independently() {
        let limits = AuthRateLimits::new(1, 1);
        assert!(limits.check_ip("127.0.0.1".to_owned()).is_ok());
        assert!(limits.check_ip("127.0.0.1".to_owned()).is_err());
        assert!(limits.check_ip("127.0.0.2".to_owned()).is_ok());
        assert!(limits.check_email("human@example.test").is_ok());
        assert!(limits.check_email("human@example.test").is_err());
        assert!(limits.check_email("other@example.test").is_ok());
        assert!(limits.check_pairing_ip("127.0.0.1".to_owned()).is_ok());
        assert!(limits.check_pairing_ip("127.0.0.1".to_owned()).is_err());
    }
}
