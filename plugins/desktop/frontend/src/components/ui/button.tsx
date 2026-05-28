import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 rounded-sm text-[12.5px] transition-[background,border-color,color,box-shadow] duration-150 disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent-bg focus-visible:border-accent",
  {
    variants: {
      variant: {
        default: "bg-panel-2 border border-border text-text hover:bg-panel-3 hover:border-border-strong",
        primary: "bg-accent border border-accent text-white hover:brightness-95 disabled:bg-panel-2 disabled:border-border disabled:text-text-faint",
        outline: "bg-transparent border border-border text-text-muted hover:text-text hover:border-border-strong hover:bg-panel-2",
        ghost: "bg-transparent border border-transparent text-text-muted hover:text-text hover:bg-panel-2",
        danger: "bg-transparent border border-border text-text-muted hover:text-error hover:border-error",
      },
      size: {
        default: "h-7 px-3",
        sm: "h-6 px-2 text-[11.5px]",
        xs: "h-5 px-2 text-[11px]",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return <Comp ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />;
  },
);
Button.displayName = "Button";

export { buttonVariants };
