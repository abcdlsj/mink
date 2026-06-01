import ReactMarkdown from "react-markdown";
import type { Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { Children, cloneElement, createContext, isValidElement, useContext } from "react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { renderMentions } from "./Mention";

interface MarkdownProps {
  children: string;
  className?: string;
  variant?: "full" | "lite";
  mentions?: Set<string>;
}

const MentionsCtx = createContext<Set<string> | null>(null);

function MentionAware({ children }: { children: ReactNode }) {
  const set = useContext(MentionsCtx);
  if (!set || set.size === 0) return <>{children}</>;
  return <>{walk(children, set)}</>;
}

function walk(children: ReactNode, set: Set<string>): ReactNode {
  return Children.map(children, (c) => {
    if (typeof c === "string") return renderMentions(c, set);
    if (isValidElement(c) && c.props && (c.props as { children?: ReactNode }).children !== undefined) {
      const inner = (c.props as { children?: ReactNode }).children;
      return cloneElement(c as React.ReactElement<{ children?: ReactNode }>, {
        children: walk(inner, set),
      });
    }
    return c;
  });
}

const fullComponents: Components = {
  p: ({ children }) => <p className="mb-2.5 last:mb-0 leading-[1.68]"><MentionAware>{children}</MentionAware></p>,
  h1: ({ children }) => <h1 className="font-display text-[16px] font-semibold mt-4 mb-1.5 first:mt-0"><MentionAware>{children}</MentionAware></h1>,
  h2: ({ children }) => <h2 className="font-display text-[15px] font-semibold mt-4 mb-1.5 first:mt-0"><MentionAware>{children}</MentionAware></h2>,
  h3: ({ children }) => <h3 className="font-display text-[14px] font-semibold mt-3 mb-1 first:mt-0"><MentionAware>{children}</MentionAware></h3>,
  h4: ({ children }) => <h4 className="font-display text-[13.5px] font-semibold mt-2.5 mb-1 first:mt-0"><MentionAware>{children}</MentionAware></h4>,
  h5: ({ children }) => <h5 className="text-[13px] font-semibold mt-2 mb-1 first:mt-0"><MentionAware>{children}</MentionAware></h5>,
  h6: ({ children }) => <h6 className="text-[12.5px] font-semibold text-text-muted mt-2 mb-1 first:mt-0"><MentionAware>{children}</MentionAware></h6>,
  ul: ({ children }) => <ul className="my-1.5 pl-5 list-disc marker:text-text-faint">{children}</ul>,
  ol: ({ children }) => <ol className="my-1.5 pl-5 list-decimal marker:text-text-faint">{children}</ol>,
  li: ({ children }) => <li className="my-1 leading-[1.68] pl-0.5"><MentionAware>{children}</MentionAware></li>,
  a: ({ href, children }) => (
    <a
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      className="text-accent hover:underline underline-offset-2 break-words"
    >
      {children}
    </a>
  ),
  blockquote: ({ children }) => (
    <blockquote className="border-l-2 border-border-strong pl-3.5 my-2.5 text-text-muted italic"><MentionAware>{children}</MentionAware></blockquote>
  ),
  code: codeRenderer,
  pre: ({ children }) => (
    <pre className="my-2.5 rounded-md bg-panel-event px-3.5 py-2.5 overflow-x-auto text-[12.5px] leading-[1.6]">
      {children}
    </pre>
  ),
  hr: () => <hr className="my-3.5 border-border-soft" />,
  table: ({ children }) => (
    <div className="my-2.5 overflow-x-auto">
      <table className="text-[13px] border-collapse w-max max-w-full">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="text-left font-display text-[11.5px] uppercase tracking-[0.5px] text-text-faint">{children}</thead>,
  th: ({ children }) => (
    <th className="border-b border-border px-2.5 py-1 font-semibold align-bottom whitespace-nowrap">{children}</th>
  ),
  td: ({ children }) => <td className="border-b border-border-soft px-2.5 py-1 align-top"><MentionAware>{children}</MentionAware></td>,
  input: ({ checked, type, ...rest }) => {
    if (type === "checkbox") {
      return (
        <input
          type="checkbox"
          checked={!!checked}
          readOnly
          className="mr-1.5 align-middle accent-accent"
          {...rest}
        />
      );
    }
    return <input type={type} {...rest} />;
  },
};

const liteComponents: Components = {
  p: ({ children }) => <span><MentionAware>{children}</MentionAware></span>,
  ul: ({ children }) => <ul className="my-1 pl-4 list-disc marker:text-text-faint">{children}</ul>,
  ol: ({ children }) => <ol className="my-1 pl-4 list-decimal marker:text-text-faint">{children}</ol>,
  li: ({ children }) => <li className="my-0.5"><MentionAware>{children}</MentionAware></li>,
  a: ({ href, children }) => (
    <a
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      className="text-accent hover:underline underline-offset-2 break-words"
    >
      {children}
    </a>
  ),
  code: codeRenderer,
  pre: ({ children }) => <span className="font-mono">{children}</span>,
  h1: ({ children }) => <span className="font-semibold"><MentionAware>{children}</MentionAware></span>,
  h2: ({ children }) => <span className="font-semibold"><MentionAware>{children}</MentionAware></span>,
  h3: ({ children }) => <span className="font-semibold"><MentionAware>{children}</MentionAware></span>,
  blockquote: ({ children }) => <span className="text-text-faint"><MentionAware>{children}</MentionAware></span>,
  table: ({ children }) => <span>{children}</span>,
};

function codeRenderer({ children, className }: any) {
  const text = String(children ?? "");
  const isBlock = (typeof className === "string" && className.startsWith("language-")) || text.includes("\n");
  if (isBlock) {
    return <code className="font-mono text-[12.5px] leading-[1.55] whitespace-pre">{children}</code>;
  }
  return (
    <code className="font-mono text-[12.5px] rounded-[3px] border border-border-soft bg-panel-event px-1 py-px">
      {children}
    </code>
  );
}

export function Markdown({ children, className, variant = "full", mentions }: MarkdownProps) {
  const components = variant === "lite" ? liteComponents : fullComponents;
  return (
    <div className={cn(className)}>
      <MentionsCtx.Provider value={mentions ?? null}>
        <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
          {children}
        </ReactMarkdown>
      </MentionsCtx.Provider>
    </div>
  );
}
