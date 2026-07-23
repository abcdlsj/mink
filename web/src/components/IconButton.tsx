import type { ButtonHTMLAttributes, ReactNode } from "react";

type TooltipPlacement = "top" | "right" | "bottom" | "left";

export function IconButton({
  label,
  tooltip = label,
  tooltipPlacement = "bottom",
  className,
  children,
  type = "button",
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "aria-label" | "title"> & {
  label: string;
  tooltip?: string;
  tooltipPlacement?: TooltipPlacement;
  children: ReactNode;
}) {
  return (
    <button
      {...props}
      type={type}
      className={["icon-button", className].filter(Boolean).join(" ")}
      aria-label={label}
      data-tooltip={tooltip}
      data-tooltip-placement={tooltipPlacement}
    >
      {children}
    </button>
  );
}
