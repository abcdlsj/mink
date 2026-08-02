import { useParams } from "@tanstack/react-router";
import { MessageCircle } from "lucide-react";

import type { Channel } from "../api/client";
import { SpaceShell } from "../components/SpaceShell";
import { MessageWorkspace } from "./ChannelPage";

export function DirectMessagePage() {
  const { spaceSlug, memberId } = useParams({ from: "/s/$spaceSlug/dm/$memberId" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="dm">
      {({ space, directMessages }) => {
        const dm = directMessages.find((candidate) => candidate.other_member.id === memberId);
        if (!dm) {
          return (
            <section className="channel-workspace">
              <div className="empty-channel">
                <span className="channel-glyph" aria-hidden="true">
                  <MessageCircle />
                </span>
                <h2>DM is not available.</h2>
              </div>
            </section>
          );
        }
        const channel: Channel = {
          id: dm.channel_id,
          space_id: dm.space_id,
          kind: "private",
          name: dm.other_member.display_name,
          slug: dm.other_member.display_name,
          created_by_member_id: "",
          joined: true,
        };
        return (
          <MessageWorkspace
            channel={channel}
            spaceId={space.id}
            title={dm.other_member.display_name}
            subtitle={`${dm.other_member.display_name} · Direct Message`}
            placeholder={`Message ${dm.other_member.display_name}`}
            emptyTitle={`Your DM with ${dm.other_member.display_name} starts here.`}
            spaceSlug={space.slug}
            direct
          />
        );
      }}
    </SpaceShell>
  );
}
