use time::OffsetDateTime;

use crate::ids::{CompanyFileId, MemberId, SpaceId};

use super::DomainError;

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct CompanyFile {
    id: CompanyFileId,
    space_id: SpaceId,
    name: String,
    media_type: String,
    length: u64,
    sha256: [u8; 32],
    uploader_member_id: MemberId,
    created_at: OffsetDateTime,
    deleted_at: Option<OffsetDateTime>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct CompanyFileSnapshot {
    pub(in crate::server) id: CompanyFileId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) name: String,
    pub(in crate::server) media_type: String,
    pub(in crate::server) length: u64,
    pub(in crate::server) sha256: [u8; 32],
    pub(in crate::server) uploader_member_id: MemberId,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) deleted_at: Option<OffsetDateTime>,
}

impl CompanyFile {
    pub(in crate::server) fn create(
        id: CompanyFileId,
        space_id: SpaceId,
        uploader_member_id: MemberId,
        name: &str,
        media_type: &str,
        length: u64,
        sha256: [u8; 32],
        now: OffsetDateTime,
    ) -> Result<Self, DomainError> {
        let name = name.trim();
        let media_type = media_type.trim();
        if name.is_empty()
            || media_type.is_empty()
            || name == "."
            || name == ".."
            || name.contains(['/', '\\'])
            || name.chars().any(|character| character.is_control())
        {
            return Err(DomainError::InvalidCompanyFile);
        }
        Ok(Self {
            id,
            space_id,
            name: name.to_owned(),
            media_type: media_type.to_owned(),
            length,
            sha256,
            uploader_member_id,
            created_at: now,
            deleted_at: None,
        })
    }

    pub(in crate::server) fn view(&self) -> CompanyFileView<'_> {
        CompanyFileView {
            id: self.id,
            space_id: self.space_id,
            name: &self.name,
            media_type: &self.media_type,
            length: self.length,
            sha256: self.sha256,
            uploader_member_id: self.uploader_member_id,
            created_at: self.created_at,
            deleted_at: self.deleted_at,
        }
    }

    pub(in crate::server) fn snapshot(&self) -> CompanyFileSnapshot {
        let view = self.view();
        CompanyFileSnapshot {
            id: view.id,
            space_id: view.space_id,
            name: view.name.to_owned(),
            media_type: view.media_type.to_owned(),
            length: view.length,
            sha256: view.sha256,
            uploader_member_id: view.uploader_member_id,
            created_at: view.created_at,
            deleted_at: view.deleted_at,
        }
    }

    pub(in crate::server) fn rehydrate(snapshot: CompanyFileSnapshot) -> Result<Self, DomainError> {
        if snapshot.name.trim().is_empty()
            || snapshot.media_type.trim().is_empty()
            || (snapshot.deleted_at.is_some() && snapshot.deleted_at < Some(snapshot.created_at))
        {
            return Err(DomainError::InvalidPersistedState);
        }
        Ok(Self {
            id: snapshot.id,
            space_id: snapshot.space_id,
            name: snapshot.name,
            media_type: snapshot.media_type,
            length: snapshot.length,
            sha256: snapshot.sha256,
            uploader_member_id: snapshot.uploader_member_id,
            created_at: snapshot.created_at,
            deleted_at: snapshot.deleted_at,
        })
    }

    pub(in crate::server) fn delete(
        &mut self,
        actor: MemberId,
        can_manage_space: bool,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.deleted_at.is_some() {
            return Err(DomainError::InvalidTransition);
        }
        if self.uploader_member_id != actor && !can_manage_space {
            return Err(DomainError::CompanyFileNotOwned);
        }
        self.deleted_at = Some(now);
        Ok(())
    }

    pub(in crate::server) fn header_safe_name(&self) -> String {
        self.name.replace(['\\', '"', '\r', '\n'], "_")
    }

    pub(in crate::server) fn object_key(&self) -> String {
        format!("spaces/{}/company/{}", self.space_id.into_uuid(), self.name)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct CompanyFileView<'a> {
    pub(in crate::server) id: CompanyFileId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) name: &'a str,
    pub(in crate::server) media_type: &'a str,
    pub(in crate::server) length: u64,
    pub(in crate::server) sha256: [u8; 32],
    pub(in crate::server) uploader_member_id: MemberId,
    pub(in crate::server) created_at: OffsetDateTime,
    pub(in crate::server) deleted_at: Option<OffsetDateTime>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use uuid::Uuid;

    fn file() -> CompanyFile {
        CompanyFile::create(
            CompanyFileId::from_uuid(Uuid::from_u128(1)),
            SpaceId::from_uuid(Uuid::from_u128(2)),
            MemberId::from_uuid(Uuid::from_u128(3)),
            "  report.pdf  ",
            "  application/pdf  ",
            4,
            [1; 32],
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("company file opens")
    }

    #[test]
    fn create_trims_and_requires_name_and_media_type() {
        assert_eq!(file().view().name, "report.pdf");
        assert_eq!(file().view().media_type, "application/pdf");
        for (name, media_type) in [("  ", "application/pdf"), ("report.pdf", " ")] {
            assert_eq!(
                CompanyFile::create(
                    CompanyFileId::from_uuid(Uuid::from_u128(1)),
                    SpaceId::from_uuid(Uuid::from_u128(2)),
                    MemberId::from_uuid(Uuid::from_u128(3)),
                    name,
                    media_type,
                    4,
                    [1; 32],
                    OffsetDateTime::UNIX_EPOCH,
                ),
                Err(DomainError::InvalidCompanyFile)
            );
        }
    }

    #[test]
    fn rehydrate_accepts_consistent_state_and_rejects_deleted_before_created() {
        let snapshot = file().snapshot();
        assert_eq!(
            CompanyFile::rehydrate(snapshot.clone()).expect("state is valid"),
            file()
        );
        let mut invalid = snapshot;
        invalid.deleted_at = Some(OffsetDateTime::UNIX_EPOCH - time::Duration::seconds(1));
        assert_eq!(
            CompanyFile::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );
    }

    #[test]
    fn only_uploader_or_governor_deletes_and_delete_is_one_way() {
        let mut file = file();
        let owner = MemberId::from_uuid(Uuid::from_u128(9));
        let now = OffsetDateTime::UNIX_EPOCH + time::Duration::seconds(5);
        assert_eq!(
            file.delete(owner, false, now),
            Err(DomainError::CompanyFileNotOwned)
        );
        file.delete(owner, true, now).expect("governor deletes");
        assert_eq!(file.view().deleted_at, Some(now));
        assert_eq!(
            file.delete(owner, true, now),
            Err(DomainError::InvalidTransition)
        );
    }

    #[test]
    fn header_safe_name_drops_header_breaking_characters() {
        let mut file = file();
        file.name = "re\"po\\rt\r\n.pdf".into();
        assert_eq!(file.header_safe_name(), "re_po_rt__.pdf");
    }
}
