import { ArrowRight } from "lucide-react";

import type { SurfaceDemo } from "./demoTypes";

function OnboardingPage({ variant }: { variant: "banner" | "center" | "split" }) {
  return (
    <div className={`demo-page demo-page--onboarding demo-page--onboarding-${variant}`}>
      {variant === "banner" && (
        <>
          <header className="demo-brand-bar"><span className="demo-rail-space">S</span><strong>SUMI</strong></header>
          <main className="demo-auth">
            <h1>Sign in.</h1>
            <div className="demo-auth-field" />
            <div className="demo-auth-field" />
            <button type="button" className="demo-auth-button">Sign in <ArrowRight aria-hidden="true" /></button>
          </main>
        </>
      )}
      {variant === "center" && (
        <main className="demo-auth demo-auth--center">
          <span className="demo-rail-space">S</span>
          <h1>Sign in.</h1>
          <div className="demo-auth-field" />
          <div className="demo-auth-field" />
          <button type="button" className="demo-auth-button">Sign in <ArrowRight aria-hidden="true" /></button>
        </main>
      )}
      {variant === "split" && (
        <main className="demo-auth demo-auth--split">
          <aside><span className="demo-rail-space">S</span><p>Human and Agent, one Space.</p></aside>
          <div>
            <h1>Sign in.</h1>
            <div className="demo-auth-field" />
            <div className="demo-auth-field" />
            <button type="button" className="demo-auth-button">Sign in <ArrowRight aria-hidden="true" /></button>
          </div>
        </main>
      )}
    </div>
  );
}

export const ONBOARDING_DEMOS: SurfaceDemo[] = [
  { id: "o1", label: "O1 · 横幅", note: "深色顶栏 + 大标题 + 左侧表单；现状基线。已采用。", Component: () => <OnboardingPage variant="banner" /> },
];
