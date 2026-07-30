use time::OffsetDateTime;

use crate::ids::{AttachmentId, MemberId, SpaceId};

use super::DomainError;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum AttachmentStatus {
    Uploading,
    Ready,
    Deleted,
}

impl AttachmentStatus {
    pub(in crate::server) fn parse(value: &str) -> Result<Self, DomainError> {
        match value {
            "uploading" => Ok(Self::Uploading),
            "ready" => Ok(Self::Ready),
            "deleted" => Ok(Self::Deleted),
            _ => Err(DomainError::InvalidAttachment),
        }
    }

    pub(in crate::server) fn code(self) -> &'static str {
        match self {
            Self::Uploading => "uploading",
            Self::Ready => "ready",
            Self::Deleted => "deleted",
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct ContentDigest {
    pub(in crate::server) length: u64,
    pub(in crate::server) sha256: [u8; 32],
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct DeclaredContent {
    pub(in crate::server) size: u64,
    pub(in crate::server) sha256_hex: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Attachment {
    pub(in crate::server) id: AttachmentId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) uploader_member_id: MemberId,
    pub(in crate::server) name: String,
    pub(in crate::server) media_type: String,
    pub(in crate::server) object_key: String,
    pub(in crate::server) status: AttachmentStatus,
    pub(in crate::server) length: Option<u64>,
    pub(in crate::server) sha256: Option<[u8; 32]>,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) ready_at: Option<OffsetDateTime>,
}

impl Attachment {
    pub(in crate::server) fn open(
        id: AttachmentId,
        space_id: SpaceId,
        uploader_member_id: MemberId,
        name: &str,
        media_type: &str,
        now: OffsetDateTime,
    ) -> Result<Self, DomainError> {
        let name = name.trim();
        let media_type = media_type.trim();
        if name.is_empty() || media_type.is_empty() {
            return Err(DomainError::InvalidAttachment);
        }
        Ok(Self {
            id,
            space_id,
            uploader_member_id,
            name: name.to_owned(),
            media_type: media_type.to_owned(),
            object_key: format!(
                "spaces/{}/attachments/{}",
                space_id.into_uuid(),
                id.into_uuid()
            ),
            status: AttachmentStatus::Uploading,
            length: None,
            sha256: None,
            created_at: now,
            ready_at: None,
        })
    }

    pub(in crate::server) fn require_uploader(&self, actor: MemberId) -> Result<(), DomainError> {
        if self.uploader_member_id == actor {
            Ok(())
        } else {
            Err(DomainError::AttachmentNotOwned)
        }
    }

    pub(in crate::server) fn require_open(&self) -> Result<(), DomainError> {
        if self.status == AttachmentStatus::Uploading {
            Ok(())
        } else {
            Err(DomainError::AttachmentNotOpen)
        }
    }

    pub(in crate::server) fn complete(
        &mut self,
        declared: &DeclaredContent,
        actual: ContentDigest,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        self.require_open()?;
        if actual.length != declared.size
            || hex::encode(actual.sha256) != declared.sha256_hex.to_lowercase()
        {
            return Err(DomainError::AttachmentContentMismatch);
        }
        self.status = AttachmentStatus::Ready;
        self.length = Some(actual.length);
        self.sha256 = Some(actual.sha256);
        self.ready_at = Some(now);
        Ok(())
    }

    pub(in crate::server) fn require_ready(&self) -> Result<(), DomainError> {
        if self.status == AttachmentStatus::Ready {
            Ok(())
        } else {
            // Callers must not learn whether an unavailable Attachment exists.
            Err(DomainError::AttachmentNotReady)
        }
    }

    pub(in crate::server) fn header_safe_name(&self) -> String {
        self.name.replace(['\\', '"', '\r', '\n'], "_")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn attachment() -> Attachment {
        Attachment::open(
            AttachmentId::from_uuid(uuid::Uuid::from_u128(1)),
            SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            MemberId::from_uuid(uuid::Uuid::from_u128(3)),
            "  report.pdf  ",
            "  application/pdf  ",
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("attachment opens")
    }

    fn digest(content: &[u8]) -> ContentDigest {
        use sha2::Digest;
        ContentDigest {
            length: content.len() as u64,
            sha256: sha2::Sha256::digest(content).into(),
        }
    }

    #[test]
    fn opening_requires_a_name_and_media_type_and_derives_the_object_key() {
        let attachment = attachment();
        assert_eq!(attachment.name, "report.pdf");
        assert_eq!(attachment.media_type, "application/pdf");
        assert_eq!(
            attachment.object_key,
            format!(
                "spaces/{}/attachments/{}",
                uuid::Uuid::from_u128(2),
                uuid::Uuid::from_u128(1)
            )
        );
        for (name, media_type) in [("  ", "application/pdf"), ("report.pdf", " ")] {
            assert_eq!(
                Attachment::open(
                    AttachmentId::from_uuid(uuid::Uuid::from_u128(1)),
                    SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
                    MemberId::from_uuid(uuid::Uuid::from_u128(3)),
                    name,
                    media_type,
                    OffsetDateTime::UNIX_EPOCH,
                ),
                Err(DomainError::InvalidAttachment)
            );
        }
    }

    #[test]
    fn completing_rejects_a_declaration_that_does_not_match_the_stored_content() {
        let content = b"actual bytes";
        let actual = digest(content);
        let mut attachment = attachment();
        let wrong_size = DeclaredContent {
            size: actual.length + 1,
            sha256_hex: hex::encode(actual.sha256),
        };
        assert_eq!(
            attachment.complete(&wrong_size, actual, OffsetDateTime::UNIX_EPOCH),
            Err(DomainError::AttachmentContentMismatch)
        );
        let wrong_hash = DeclaredContent {
            size: actual.length,
            sha256_hex: hex::encode([0u8; 32]),
        };
        assert_eq!(
            attachment.complete(&wrong_hash, actual, OffsetDateTime::UNIX_EPOCH),
            Err(DomainError::AttachmentContentMismatch)
        );
        assert_eq!(attachment.status, AttachmentStatus::Uploading);
        assert_eq!(attachment.length, None);
    }

    #[test]
    fn completing_accepts_an_uppercase_declaration_and_closes_the_upload_window() {
        let content = b"actual bytes";
        let actual = digest(content);
        let mut attachment = attachment();
        let declared = DeclaredContent {
            size: actual.length,
            sha256_hex: hex::encode(actual.sha256).to_uppercase(),
        };
        let now = OffsetDateTime::UNIX_EPOCH + time::Duration::seconds(5);
        attachment
            .complete(&declared, actual, now)
            .expect("declaration matches");
        assert_eq!(attachment.status, AttachmentStatus::Ready);
        assert_eq!(attachment.length, Some(actual.length));
        assert_eq!(attachment.ready_at, Some(now));
        attachment.require_ready().expect("ready");
        assert_eq!(
            attachment.complete(&declared, actual, now),
            Err(DomainError::AttachmentNotOpen)
        );
    }

    #[test]
    fn only_the_uploader_writes_and_an_unready_attachment_cannot_be_downloaded() {
        let attachment = attachment();
        attachment
            .require_uploader(MemberId::from_uuid(uuid::Uuid::from_u128(3)))
            .expect("uploader matches");
        assert_eq!(
            attachment.require_uploader(MemberId::from_uuid(uuid::Uuid::from_u128(9))),
            Err(DomainError::AttachmentNotOwned)
        );
        assert_eq!(
            attachment.require_ready(),
            Err(DomainError::AttachmentNotReady)
        );
    }

    #[test]
    fn the_header_safe_name_drops_characters_that_would_break_the_header() {
        let mut attachment = attachment();
        attachment.name = "re\"po\\rt\r\n.pdf".into();
        assert_eq!(attachment.header_safe_name(), "re_po_rt__.pdf");
    }
}
