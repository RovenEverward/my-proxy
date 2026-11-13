import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import { cn } from "@/lib/utils";

// shadcn's Slot/CVA contract uses the product's semantic control styles.
const buttonVariants = cva("btn", {
  variants: {
    variant: {
      default: "", primary: "pri", danger: "danger", destructive: "danger",
      secondary: "", ghost: "ghost", outline: "", link: "lnk",
    },
    size: { default: "", sm: "sm", icon: "ib", "icon-sm": "ib sm", lg: "" },
  },
  defaultVariants: { variant: "default", size: "default" },
});
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean;
  loading?: boolean;
}
const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(function Button({ className, variant, size, asChild = false, loading, disabled, type = "button", ...props }, ref) {
  const Comp = asChild ? Slot.Root : "button";
  const classes = buttonVariants({ variant, size });
  return <Comp ref={ref} type={type} data-slot="button" data-variant={variant} data-size={size}
    className={cn(size?.startsWith("icon") ? classes.replace(/^btn\b\s*/, "") : classes, loading && "busy", className)} disabled={disabled || loading}
    aria-busy={loading || undefined} {...props} />;
});
export { Button, buttonVariants };
