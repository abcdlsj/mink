import ReactMarkdown from "react-markdown";
import type { Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "@/lib/utils";

interface MarkdownProps {
  children: string;
  className?: string;
  variant?: "full" | "lite";
}

const fullComponents: Components = {
  p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
  h1: ({ children }) => <h1 className="text-[16px] font-semibold mt-3 mb-1.5 first:mt-0">{children}</h1>,
  h2: ({ children }) => <h2 className="text-[15px] font-semibold mt-3 mb-1.5 first:mt-0">{children}</h2>,
  h3: ({ children }) => <h3 className="text-[14px] font-semibold mt-2.5 mb-1 first:mt-0">{children}</h3>,
  h4: ({ children }) => <h4 className="text-[13.5px] font-semibold mt-2 mb-1 first:mt-0">{children}</h4>,
  h5: ({ children }) => <h5 className="text-[13px] font-semibold mt-2 mb-1 first:mt-0">{children}</h5>,
  h6: ({ children }) => <h6 className="text-[12.5px] font-semibold text-text-muted mt-2 mb-1 first:mt-0">{children}</h6>,
  ul: ({ children }) => <ul className="my-1 pl-5 list-disc marker:text-text-faint">{children}</ul>,
  ol: ({ children }) => <ol className="my-1 pl-5 list-decimal marker:text-text-faint">{children}</ol>,
  li: ({ children }) => <li className="my-0.5 leading-[1.65]">{children}</li>,
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
    <blockquote className="border-l-2 border-border-strong pl-3 my-1.5 text-text-muted">{children}</blockquote>
  ),
  code: codeRenderer,
  pre: ({ children }) => (
    <pre className="my-2 rounded-md border border-border-soft bg-panel-event px-3 py-2 overflow-x-auto text-[12.5px] leading-[1.55]">
      {children}
    </pre>
  ),
  hr: () => <hr className="my-3 border-border-soft" />,
  table: ({ children }) => (
    <div className="my-2 overflow-x-auto">
      <table className="text-[13px] border-collapse w-max max-w-full">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="text-left text-[12px] text-text-muted">{children}</thead>,
  th: ({ children }) => (
    <th className="border-b border-border px-2 py-1 font-semibold align-bottom whitespace-nowrap">{children}</th>
  ),
  td: ({ children }) => <td className="border-b border-border-soft px-2 py-1 align-top">{children}</td>,
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
  p: ({ children }) => <span>{children}</span>,
  ul: ({ children }) => <ul className="my-1 pl-4 list-disc marker:text-text-faint">{children}</ul>,
  ol: ({ children }) => <ol className="my-1 pl-4 list-decimal marker:text-text-faint">{children}</ol>,
  li: ({ children }) => <li className="my-0.5">{children}</li>,
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
  h1: ({ children }) => <span className="font-semibold">{children}</span>,
  h2: ({ children }) => <span className="font-semibold">{children}</span>,
  h3: ({ children }) => <span className="font-semibold">{children}</span>,
  blockquote: ({ children }) => <span className="text-text-faint">{children}</span>,
  table: ({ children }) => <span>{children}</span>,
};

function codeRenderer({ inline, children, className: _className }: any) {
  if (inline === false) {
    return (
      <code className="block font-mono text-[12.5px] leading-[1.55] whitespace-pre">
        {children}
      </code>
    );
  }
  return (
    <code className="font-mono text-[12.5px] rounded-[3px] border border-border-soft bg-panel-event px-1 py-px">
      {children}
    </code>
  );
}

export function Markdown({ children, className, variant = "full" }: MarkdownProps) {
  const components = variant === "lite" ? liteComponents : fullComponents;
  return (
    <div className={cn(className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  );
}
