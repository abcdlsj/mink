import { useParams } from "@tanstack/react-router";

import { SpaceShell } from "../components/SpaceShell";
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
