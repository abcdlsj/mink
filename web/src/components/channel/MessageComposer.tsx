import { useMutation } from "@tanstack/react-query";
import { LoaderCircle, Paperclip, Send, X } from "lucide-react";
import { type ChangeEvent, type FormEvent, type KeyboardEvent, useEffect, useRef, useState } from "react";

import { uploadAttachment, type Member, type Message } from "../../api/client";
import { PixelIdentity } from "../SpaceShell";

export interface ComposerInput {
  body_markdown: string;
  mentions: string[];
  mention_all: boolean;
  attachment_ids: string[];
}

export function MessageComposer({
  spaceId,
  members,
  placeholder,
  ariaLabel,
  attachmentAriaLabel,
  attachButtonLabel,
  sendButtonLabel,
  attachmentsAriaLabel,
  className,
  send,
  onSent,
}: {
  spaceId: string;
  members: Member[];
  placeholder: string;
  ariaLabel: string;
  attachmentAriaLabel: string;
  attachButtonLabel: string;
  sendButtonLabel: string;
  attachmentsAriaLabel: string;
  className?: string;
  send: (input: ComposerInput) => Promise<Message>;
  onSent: (message: Message) => void;
}) {
  const [body, setBody] = useState("");
  const [attachments, setAttachments] = useState<Awaited<ReturnType<typeof uploadAttachment>>[]>([]);
  const fileInput = useRef<HTMLInputElement>(null);
  const submission = useMutation({
    mutationFn: send,
    onSuccess: (message) => {
      onSent(message);
      setBody("");
      setAttachments([]);
    },
  });
  const upload = useMutation({
    mutationFn: (file: File) => uploadAttachment(spaceId, file),
    onSuccess: (attachment) => setAttachments((current) => [...current, attachment]),
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = body.trim();
    if (!trimmed) return;
    submission.mutate({
      body_markdown: trimmed,
      mentions: mentionIds(trimmed, members),
      mention_all: /(?:^|\s)@all(?![a-z0-9-])/i.test(trimmed),
      attachment_ids: attachments.map((attachment) => attachment.id),
    });
  }

  function selectFile(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (file) upload.mutate(file);
    event.target.value = "";
  }

  return (
    <form className={`composer${className ? ` ${className}` : ""}`} onSubmit={submit}>
      <input
        ref={fileInput}
        className="visually-hidden"
        type="file"
        aria-label={attachmentAriaLabel}
        onChange={selectFile}
      />
      <button
        className="icon-button composer-attach-button"
        type="button"
        aria-label={attachButtonLabel}
        title={attachButtonLabel}
        disabled={upload.isPending}
        onClick={() => fileInput.current?.click()}
      >
        {upload.isPending ? <LoaderCircle className="spin" /> : <Paperclip />}
      </button>
      <MentionInput
        ariaLabel={ariaLabel}
        placeholder={placeholder}
        rows={1}
        value={body}
        members={members}
        onChange={setBody}
      />
      <button
        className="send-button"
        type="submit"
        aria-label={sendButtonLabel}
        title={sendButtonLabel}
        disabled={submission.isPending || upload.isPending || !body.trim()}
      >
        {submission.isPending ? <LoaderCircle className="spin" /> : <><Send /><span>Send</span></>}
      </button>
      <span className="composer-shortcut">⌘ ENTER TO SEND</span>
      {attachments.length ? (
        <div className="composer-attachments" aria-label={attachmentsAriaLabel}>
          {attachments.map((attachment) => (
            <span key={attachment.id}>
              <Paperclip aria-hidden="true" />
              {attachment.original_name}
              <button
                type="button"
                aria-label={`Remove ${attachment.original_name}`}
                onClick={() =>
                  setAttachments((current) => current.filter((item) => item.id !== attachment.id))
                }
              >
                <X aria-hidden="true" />
              </button>
            </span>
          ))}
        </div>
      ) : null}
      {submission.error || upload.error ? (
        <p className="composer-error" role="alert">
          {submission.error?.message ?? upload.error?.message}
        </p>
      ) : null}
    </form>
  );
}

function MentionInput({ ariaLabel, placeholder, rows, value, members, onChange }: { ariaLabel: string; placeholder: string; rows: number; value: string; members: Member[]; onChange: (value: string) => void }) {
  const textarea = useRef<HTMLTextAreaElement>(null);
  const [cursor, setCursor] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);
  const match = mentionMatch(value, cursor);
  const suggestions = match ? members.filter((member) => {
    const query = match.query.toLowerCase();
    return member.handle.toLowerCase().includes(query) || member.display_name.toLowerCase().includes(query);
  }).slice(0, 6) : [];
  const allSuggestion = Boolean(match && "all".startsWith(match.query.toLowerCase()));

  useEffect(() => {
    const input = textarea.current;
    if (!input) return;
    const minimumHeight = rows > 1 ? 54 : 42;
    input.style.height = "auto";
    const contentHeight = value ? input.scrollHeight : minimumHeight;
    input.style.height = `${Math.min(Math.max(contentHeight, minimumHeight), 240)}px`;
  }, [rows, value]);

  function choose(member: Member) {
    if (!match) return;
    const inserted = `@${member.handle} `;
    const next = `${value.slice(0, match.start)}${inserted}${value.slice(cursor)}`;
    const nextCursor = match.start + inserted.length;
    onChange(next);
    setCursor(nextCursor);
    window.requestAnimationFrame(() => {
      textarea.current?.focus();
      textarea.current?.setSelectionRange(nextCursor, nextCursor);
    });
  }

  function chooseAll() {
    if (!match) return;
    const inserted = "@all ";
    const next = `${value.slice(0, match.start)}${inserted}${value.slice(cursor)}`;
    const nextCursor = match.start + inserted.length;
    onChange(next);
    setCursor(nextCursor);
    window.requestAnimationFrame(() => {
      textarea.current?.focus();
      textarea.current?.setSelectionRange(nextCursor, nextCursor);
    });
  }

  function handleKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      event.currentTarget.form?.requestSubmit();
      return;
    }
    if (!suggestions.length && !allSuggestion) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const direction = event.key === "ArrowDown" ? 1 : -1;
      const count = suggestions.length + (allSuggestion ? 1 : 0);
      setActiveIndex((index) => (index + direction + count) % count);
    } else if (event.key === "Enter" || event.key === "Tab") {
      event.preventDefault();
      if (allSuggestion && activeIndex === 0) {
        chooseAll();
      } else {
        choose(suggestions[Math.min(Math.max(activeIndex - (allSuggestion ? 1 : 0), 0), suggestions.length - 1)]);
      }
    } else if (event.key === "Escape") {
      setCursor(-1);
    }
  }

  return (
    <div className="mention-input">
      {suggestions.length || allSuggestion ? (
        <div className="mention-suggestions" role="listbox" aria-label="Mention suggestions">
          {allSuggestion ? (
            <button className="mention-suggestion-all" type="button" role="option" aria-selected={activeIndex === 0} onMouseDown={(event) => event.preventDefault()} onClick={chooseAll}>
              <span><strong>Everyone</strong><small>@all</small></span>
            </button>
          ) : null}
          {suggestions.map((member, index) => (
            <button key={member.id} type="button" role="option" aria-selected={index + (allSuggestion ? 1 : 0) === activeIndex} onMouseDown={(event) => event.preventDefault()} onClick={() => choose(member)}>
              <PixelIdentity name={member.display_name} kind={member.kind} seed={member.id} />
              <span><strong>{member.display_name}</strong><small>@{member.handle}</small></span>
              {member.kind === "agent" ? <span className="agent-label">AGENT</span> : null}
            </button>
          ))}
        </div>
      ) : null}
      <textarea
        ref={textarea}
        aria-label={ariaLabel}
        placeholder={placeholder}
        rows={rows}
        value={value}
        maxLength={20_000}
        onClick={(event) => setCursor(event.currentTarget.selectionStart)}
        onSelect={(event) => setCursor(event.currentTarget.selectionStart)}
        onChange={(event) => { onChange(event.target.value); setCursor(event.target.selectionStart); setActiveIndex(0); }}
        onKeyDown={handleKey}
      />
    </div>
  );
}

function mentionMatch(value: string, cursor: number): { start: number; query: string } | undefined {
  if (cursor < 0) return undefined;
  const prefix = value.slice(0, cursor);
  const match = prefix.match(/(?:^|\s)@([a-z0-9-]*)$/i);
  if (!match) return undefined;
  return { start: cursor - match[1].length - 1, query: match[1] };
}


function mentionIds(body: string, members: Member[]): string[] {
  const byHandle = new Map(members.map((member) => [member.handle.toLowerCase(), member.id]));
  const ids = new Set<string>();
  for (const match of body.matchAll(/(^|\s)@([a-z0-9]+(?:-[a-z0-9]+)*)/gi)) {
    const id = byHandle.get(match[2].toLowerCase());
    if (id) ids.add(id);
  }
  return [...ids];
}
