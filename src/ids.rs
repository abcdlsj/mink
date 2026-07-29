use std::{fmt, str::FromStr};

use serde::{Deserialize, Serialize};
use uuid::Uuid;

macro_rules! define_id {
    ($($name:ident),+ $(,)?) => {
        $(
            #[derive(Clone, Copy, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
            #[serde(transparent)]
            pub(crate) struct $name(Uuid);

            impl $name {
                pub(crate) const fn from_uuid(value: Uuid) -> Self {
                    Self(value)
                }

                pub(crate) const fn into_uuid(self) -> Uuid {
                    self.0
                }
            }

            impl fmt::Debug for $name {
                fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                    fmt::Display::fmt(self, formatter)
                }
            }

            impl fmt::Display for $name {
                fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                    self.0.fmt(formatter)
                }
            }

            impl FromStr for $name {
                type Err = uuid::Error;

                fn from_str(value: &str) -> Result<Self, Self::Err> {
                    Uuid::parse_str(value).map(Self)
                }
            }
        )+
    };
}

define_id!(
    AgentId,
    AttachmentId,
    ChannelId,
    CommandId,
    ComputerId,
    DaemonSessionId,
    EventId,
    IdempotencyKey,
    InboxItemId,
    MemberId,
    MessageId,
    NoticeId,
    RunId,
    SpaceId,
    TaskId,
    ThreadId,
);
