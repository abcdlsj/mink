use serde::{Deserialize, Serialize};

pub(crate) const CURRENT: ProtocolVersion = ProtocolVersion::new(4);
pub(crate) const SUPPORTED: ProtocolVersionRange = ProtocolVersionRange::new(CURRENT, CURRENT);

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(transparent)]
pub(crate) struct ProtocolVersion(u16);

impl ProtocolVersion {
    pub(crate) const fn new(value: u16) -> Self {
        Self(value)
    }

    pub(crate) const fn value(self) -> u16 {
        self.0
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct ProtocolVersionRange {
    pub(crate) minimum: ProtocolVersion,
    pub(crate) maximum: ProtocolVersion,
}

impl ProtocolVersionRange {
    pub(crate) const fn new(minimum: ProtocolVersion, maximum: ProtocolVersion) -> Self {
        Self { minimum, maximum }
    }

    pub(crate) fn negotiate(self, peer: Self) -> Option<ProtocolVersion> {
        let minimum = self.minimum.max(peer.minimum);
        let maximum = self.maximum.min(peer.maximum);
        (minimum <= maximum).then_some(maximum)
    }
}

#[cfg(test)]
mod tests {
    use super::{ProtocolVersion, ProtocolVersionRange};

    #[test]
    fn negotiation_chooses_highest_shared_version() {
        let server = ProtocolVersionRange::new(ProtocolVersion::new(1), ProtocolVersion::new(3));
        let computer = ProtocolVersionRange::new(ProtocolVersion::new(2), ProtocolVersion::new(4));

        assert_eq!(server.negotiate(computer), Some(ProtocolVersion::new(3)));
    }

    #[test]
    fn negotiation_rejects_disjoint_ranges() {
        let server = ProtocolVersionRange::new(ProtocolVersion::new(1), ProtocolVersion::new(2));
        let computer = ProtocolVersionRange::new(ProtocolVersion::new(3), ProtocolVersion::new(4));

        assert_eq!(server.negotiate(computer), None);
    }
}
