import type { ComponentType } from "react";

export interface SurfaceDemo {
  id: string;
  label: string;
  note: string;
  Component: ComponentType;
}
