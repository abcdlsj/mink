import { useMemo } from "react";
import { identiconSVG, type IdenticonKind } from "@/lib/identicon";
import { cn } from "@/lib/utils";

interface IdenticonProps {
  seed: string;
  kind?: IdenticonKind;
  className?: string;
}

export function Identicon({ seed, kind = "agent", className }: IdenticonProps) {
  const svg = useMemo(() => identiconSVG(seed, kind), [seed, kind]);
  return (
    <span
      className={cn("block w-full h-full", className)}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
