import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { LoaderCircle } from "lucide-react";

import { joinChannel } from "../api/client";
import { MessageWorkspace } from "./ChannelPage";
import { SpaceShell } from "../components/SpaceShell";
import { CompanyDriveView } from "./company/DriveView";
import { CompanyOfficeView } from "./company/OfficeView";

export function CompanyOfficePage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/company/office" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="company">
      {({ space, channels, directMessages, members, currentMember }) => (
        <CompanyOfficeView
          spaceSlug={space.slug}
          spaceId={space.id}
          channels={channels}
          directMessages={directMessages}
          members={members}
          currentMember={currentMember}
        />
      )}
    </SpaceShell>
  );
}

export function CompanyHQPage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/company/hq" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="company">
      {({ space, channels }) => {
        const hq = channels.find((channel) => channel.slug === "hq");
        if (!hq) {
          return (
            <div className="company-hq-status">
              <h1>HQ</h1>
              <p>#hq is not available for this Space.</p>
            </div>
          );
        }
        if (!hq.joined) {
          return <HqJoinGate spaceId={space.id} hqId={hq.id} />;
        }
        return (
          <MessageWorkspace
            key={hq.id}
            channel={hq}
            spaceId={space.id}
            title="#hq"
            subtitle={hq.topic ?? "Company-wide conversation"}
            placeholder="Message #hq"
            emptyTitle="#hq starts here."
            spaceSlug={space.slug}
          />
        );
      }}
    </SpaceShell>
  );
}

function HqJoinGate({ spaceId, hqId }: { spaceId: string; hqId: string }) {
  const queryClient = useQueryClient();
  const join = useMutation({
    mutationFn: joinChannel,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["channels", spaceId] });
    },
  });
  return (
    <div className="company-hq-status">
      <h1>HQ</h1>
      <p>#hq is auto-joined by every Member and Agent.</p>
      <button
        className="command-button command-button--accent"
        type="button"
        disabled={join.isPending}
        onClick={() => join.mutate(hqId)}
      >
        {join.isPending ? <LoaderCircle className="spin" aria-hidden="true" /> : null}
        Join #hq
      </button>
    </div>
  );
}

export function CompanyDrivePage() {
  const { spaceSlug } = useParams({ from: "/s/$spaceSlug/company/drive" });
  return (
    <SpaceShell spaceSlug={spaceSlug} active="company">
      {({ space, currentMember }) => (
        <CompanyDriveView
          spaceSlug={space.slug}
          spaceId={space.id}
          currentMember={currentMember}
        />
      )}
    </SpaceShell>
  );
}
