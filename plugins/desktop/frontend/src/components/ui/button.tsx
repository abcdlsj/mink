import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import * as React from "react";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 rounded-sm border-hard text-[12.5px] font-medium transition-[background,color,transform,box-shadow] duration-100 disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent-bg focus-visible:border-border",
  {
    variants: {
      variant: {
        default: "bg-panel text-text hover:bg-accent hover:shadow-card active:translate-x-px active:translate-y-px active:shadow-none",
        primary: "bg-action text-text hover:shadow-card disabled:bg-panel-3 disabled:text-text-faint disabled:opacity-100",
        outline: "bg-transparent text-text-muted hover:bg-accent hover:text-text hover:shadow-card",
        ghost: "border-transparent bg-transparent text-text-muted hover:border-border hover:bg-panel-2 hover:text-text",
        danger: "bg-transparent text-text-muted hover:border-error hover:bg-action-bg hover:text-error",
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
