import { useEffect, useMemo, useState, type FormEvent } from "react";
import type { Agent, AgentRuntimeSpec } from "../gen/sumi/agent/v1/agent_pb";
import {
  EngineKind,
  ProviderProtocol,
  RuntimeSandboxProvider,
} from "../gen/sumi/agent/v1/agent_pb";
import {
  CapabilityHealth,
  CredentialDeliveryAlgorithm,
  CredentialDeliveryState,
  CredentialKind,
  type Computer,
  type CredentialDelivery,
  type EngineCapability,
} from "../gen/sumi/computer/v1/computer_pb";
import type { AgentPlacement } from "../gen/sumi/placement/v1/placement_pb";
import { sealCredential } from "../lib/credentialDelivery";
import {
  enqueueCredentialDelivery,
  factErrorMessage,
  getAgentRuntimeSpec,
  listCredentialDeliveries,
  setAgentPlacement,
  updateAgentRuntimeSpec,
  waitForCredentialDelivery,
} from "../lib/facts";
import { engineKindLabel } from "../lib/format";
import { InlineNotice } from "./ManagementFeedback";

type SubmissionState =
  | "idle"
  | "sealing"
  | "delivering"
  | "saving"
  | "placing"
  | "succeeded"
  | "failed";

export function RuntimeConfiguration({
  agent,
  placement,
  computers,
  onConfigured,
}: {
  agent: Agent;
  placement?: AgentPlacement;
  computers: Computer[];
  onConfigured: () => void;
}) {
  const initialComputer = placement?.computerId ?? computers[0]?.id ?? "";
  const [computerId, setComputerId] = useState(initialComputer);
  const [engine, setEngine] = useState(
    placement?.runtimeSpec?.engine ?? EngineKind.UNSPECIFIED,
  );
  const [protocol, setProtocol] = useState(
    placement?.runtimeSpec?.providerProtocol ?? ProviderProtocol.UNSPECIFIED,
  );
  const [endpoint, setEndpoint] = useState(
    placement?.runtimeSpec?.providerEndpoint ?? "",
  );
  const [model, setModel] = useState(placement?.runtimeSpec?.model ?? "");
  const [rawCredential, setRawCredential] = useState("");
  const [runtimeSpec, setRuntimeSpec] = useState<AgentRuntimeSpec | undefined>(
    placement?.runtimeSpec,
  );
  const [deliveries, setDeliveries] = useState<CredentialDelivery[]>([]);
  const [deliveryLoadError, setDeliveryLoadError] = useState("");
  const [submission, setSubmission] = useState<SubmissionState>("idle");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [runtimeCommitted, setRuntimeCommitted] = useState(false);
  const [messageTool, setMessageTool] = useState(
    placement?.runtimeSpec?.toolPolicy?.message ?? false,
  );
  const [workTool, setWorkTool] = useState(
    placement?.runtimeSpec?.toolPolicy?.work ?? false,
  );
  const [knowledgeTool, setKnowledgeTool] = useState(
    placement?.runtimeSpec?.toolPolicy?.knowledge ?? false,
  );

  const computer = computers.find((item) => item.id === computerId);
  const engines = useMemo(
    () =>
      (computer?.capabilityInventory?.engines ?? []).filter(
        (item) => item.health === CapabilityHealth.HEALTHY,
      ),
    [computer],
  );
  const engineCapability = engines.find((item) => item.engine === engine);
  const protocols = engineCapability?.providerProtocols ?? [];
  const credentialKind = credentialKindFor(engine, protocol);
  const reusableBinding = deliveries.find(
    (item) =>
      item.credentialKind === credentialKind &&
      item.state === CredentialDeliveryState.SUCCEEDED &&
      item.bindingHandle !== "",
  );
  const latestDelivery = deliveries.find(
    (item) => item.credentialKind === credentialKind,
  );
  const deliveryCapability = computer?.capabilityInventory?.credentialDelivery;
  const deliveryReady =
    deliveryCapability?.health === CapabilityHealth.HEALTHY &&
    deliveryCapability.algorithm ===
      CredentialDeliveryAlgorithm.X25519_XCHACHA20_POLY1305 &&
    deliveryCapability.keyId !== "" &&
    deliveryCapability.publicKey.length === 32;
  const busy =
    submission === "sealing" ||
    submission === "delivering" ||
    submission === "saving" ||
    submission === "placing";

  useEffect(() => {
    setComputerId(placement?.computerId ?? computers[0]?.id ?? "");
  }, [agent.id, placement?.computerId]);

  useEffect(() => {
    let current = true;
    void getAgentRuntimeSpec(agent.id)
      .then((spec) => {
        if (!current || !spec) return;
        setRuntimeSpec(spec);
        setEngine(spec.engine);
        setProtocol(spec.providerProtocol);
        setEndpoint(spec.providerEndpoint);
        setModel(spec.model);
        setMessageTool(spec.toolPolicy?.message ?? false);
        setWorkTool(spec.toolPolicy?.work ?? false);
        setKnowledgeTool(spec.toolPolicy?.knowledge ?? false);
      })
      .catch((loadError) => {
        if (current) {
          setError(factErrorMessage(loadError, "load runtime configuration"));
        }
      });
    return () => {
      current = false;
    };
  }, [agent.id]);

  useEffect(() => {
    if (!computerId) {
      setDeliveries([]);
      return;
    }
    let current = true;
    setDeliveryLoadError("");
    void listCredentialDeliveries({ computerId, agentId: agent.id })
      .then((items) => {
        if (current) setDeliveries(items);
      })
      .catch((loadError) => {
        if (current) {
          setDeliveryLoadError(
            factErrorMessage(loadError, "load credential bindings"),
          );
        }
      });
    return () => {
      current = false;
    };
  }, [agent.id, computerId]);

  useEffect(() => {
    if (engines.length === 0) {
      setEngine(EngineKind.UNSPECIFIED);
      setProtocol(ProviderProtocol.UNSPECIFIED);
      return;
    }
    if (!engines.some((item) => item.engine === engine)) {
      selectEngine(engines[0]);
    }
  }, [engines]);

  function selectEngine(capability: EngineCapability) {
    setEngine(capability.engine);
    if (capability.engine === EngineKind.BUILTIN) {
      const nextProtocol =
        capability.providerProtocols[0] ?? ProviderProtocol.UNSPECIFIED;
      setProtocol(nextProtocol);
      setEndpoint(defaultEndpoint(nextProtocol));
    } else {
      setProtocol(ProviderProtocol.UNSPECIFIED);
      setEndpoint("");
      setModel("");
    }
    markDirty();
  }

  function markDirty() {
    setRuntimeCommitted(false);
    setSubmission("idle");
    setError("");
    setSuccess("");
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    setSuccess("");
    if (!computer || !engineCapability) {
      setSubmission("failed");
      setError("Select a Computer with a healthy Engine.");
      return;
    }
    if (!deliveryReady || credentialKind === CredentialKind.UNSPECIFIED) {
      setSubmission("failed");
      setError(
        "This Computer cannot receive and securely store credentials for the selected Engine.",
      );
      return;
    }
    if (engine === EngineKind.BUILTIN) {
      if (!protocols.includes(protocol)) {
        setSubmission("failed");
        setError("Select a Provider protocol declared by this Computer.");
        return;
      }
      try {
        const parsed = new URL(endpoint);
        if (
          parsed.protocol !== "https:" ||
          parsed.username ||
          parsed.password
        ) {
          throw new Error("invalid endpoint");
        }
      } catch {
        setSubmission("failed");
        setError("Provider endpoint must be a credential-free HTTPS URL.");
        return;
      }
      if (!model.trim()) {
        setSubmission("failed");
        setError("Builtin Engine requires a model.");
        return;
      }
    }

    try {
      let bindingHandle = reusableBinding?.bindingHandle ?? "";
      if (!runtimeCommitted) {
        if (rawCredential) {
          setSubmission("sealing");
          const requestId = crypto.randomUUID();
          const expiresAt = new Date(Date.now() + 5 * 60_000);
          const sealedCredential = sealCredential(
            deliveryCapability.publicKey,
            {
              requestId,
              computerId: computer.id,
              agentId: agent.id,
              credentialKind: credentialKindName(credentialKind),
              keyId: deliveryCapability.keyId,
              expiresAt,
            },
            rawCredential,
          );
          setRawCredential("");
          setSubmission("delivering");
          let delivery = await enqueueCredentialDelivery({
            requestId,
            computerId: computer.id,
            agentId: agent.id,
            credentialKind,
            sealedCredential,
            expiresAt,
          });
          if (delivery.state !== CredentialDeliveryState.SUCCEEDED) {
            delivery = await waitForCredentialDelivery({
              deliveryId: delivery.id,
              computerId: computer.id,
              agentId: agent.id,
            });
          }
          bindingHandle = delivery.bindingHandle;
          setDeliveries((current) => [
            delivery,
            ...current.filter((item) => item.id !== delivery.id),
          ]);
        }
        if (!bindingHandle) {
          setSubmission("failed");
          setError(
            "Enter a credential or wait for an existing secure binding to succeed.",
          );
          return;
        }

        setSubmission("saving");
        const currentSpec = await getAgentRuntimeSpec(agent.id);
        const updated = await updateAgentRuntimeSpec({
          requestId: crypto.randomUUID(),
          agentId: agent.id,
          expectedRevision: currentSpec?.revision ?? 0n,
          engine,
          providerProtocol:
            engine === EngineKind.BUILTIN
              ? protocol
              : ProviderProtocol.UNSPECIFIED,
          providerEndpoint: engine === EngineKind.BUILTIN ? endpoint : "",
          model: engine === EngineKind.BUILTIN ? model.trim() : "",
          credentialBindingHandle: bindingHandle,
          sandboxProvider: RuntimeSandboxProvider.TRUSTED_LOCAL,
          maxRunDurationSeconds: 900,
          maxOutputBytes: 8n << 20n,
          toolPolicy: {
            message: engineCapability.supportsToolCalls && messageTool,
            work: engineCapability.supportsToolCalls && workTool,
            artifact: false,
            knowledge: engineCapability.supportsToolCalls && knowledgeTool,
          },
        });
        setRuntimeSpec(updated);
        setRuntimeCommitted(true);
      }

      setSubmission("placing");
      const updatedPlacement = await setAgentPlacement({
        requestId: crypto.randomUUID(),
        agentId: agent.id,
        computerId: computer.id,
      });
      setSubmission("succeeded");
      setRuntimeCommitted(false);
      setSuccess(
        `Runtime revision ${updatedPlacement.runtimeSpec?.revision.toString() ?? runtimeSpec?.revision.toString() ?? "unknown"} committed; desired revision ${updatedPlacement.desiredRevision.toString()} is ${placementStateText(updatedPlacement)}.`,
      );
      onConfigured();
    } catch (submitError) {
      setSubmission("failed");
      setError(factErrorMessage(submitError, "configure Agent runtime"));
    }
  }

  return (
    <section className="detail-section runtime-configuration">
      <header>
        <div>
          <span className="eyebrow">Runtime control</span>
          <h3>Engine and credential</h3>
        </div>
        <span className="revision-label">
          Runtime revision{" "}
          {runtimeSpec?.revision.toString() ?? "not configured"}
        </span>
      </header>
      {deliveryLoadError && (
        <InlineNotice
          tone="warning"
          title="Credential facts unavailable"
          detail={deliveryLoadError}
        />
      )}
      {latestDelivery && (
        <InlineNotice
          tone={
            latestDelivery.state === CredentialDeliveryState.SUCCEEDED
              ? "success"
              : latestDelivery.state === CredentialDeliveryState.FAILED ||
                  latestDelivery.state === CredentialDeliveryState.EXPIRED
                ? "danger"
                : "warning"
          }
          title={`Credential ${credentialStateText(latestDelivery.state)}`}
          detail={
            latestDelivery.errorCode ||
            (latestDelivery.bindingHandle
              ? "A non-secret binding is ready for reuse."
              : "The Computer daemon is processing the sealed payload.")
          }
        />
      )}
      <form className="runtime-form" onSubmit={submit}>
        <label className="full-field">
          <span>Computer</span>
          <select
            aria-label="Runtime Computer"
            disabled={busy}
            value={computerId}
            onChange={(event) => {
              setComputerId(event.target.value);
              markDirty();
            }}
          >
            <option value="">Select a Computer</option>
            {computers.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>Engine</span>
          <select
            aria-label="Runtime Engine"
            disabled={busy || engines.length === 0}
            value={engine}
            onChange={(event) => {
              const next = engines.find(
                (item) => item.engine === Number(event.target.value),
              );
              if (next) selectEngine(next);
            }}
          >
            {engines.length === 0 && (
              <option value={EngineKind.UNSPECIFIED}>No healthy Engine</option>
            )}
            {engines.map((item) => (
              <option key={item.engine} value={item.engine}>
                {engineKindLabel(item.engine)} · {item.version}
              </option>
            ))}
          </select>
        </label>
        {engine === EngineKind.BUILTIN && (
          <>
            <label>
              <span>Provider protocol</span>
              <select
                aria-label="Provider protocol"
                disabled={busy}
                value={protocol}
                onChange={(event) => {
                  const next = Number(event.target.value) as ProviderProtocol;
                  setProtocol(next);
                  setEndpoint(defaultEndpoint(next));
                  markDirty();
                }}
              >
                {protocols.map((item) => (
                  <option key={item} value={item}>
                    {providerProtocolText(item)}
                  </option>
                ))}
              </select>
            </label>
            <label className="full-field">
              <span>Provider endpoint</span>
              <input
                aria-label="Provider endpoint"
                disabled={busy}
                inputMode="url"
                value={endpoint}
                onChange={(event) => {
                  setEndpoint(event.target.value);
                  markDirty();
                }}
              />
            </label>
            <label>
              <span>Model</span>
              <input
                aria-label="Provider model"
                disabled={busy}
                value={model}
                onChange={(event) => {
                  setModel(event.target.value);
                  markDirty();
                }}
              />
            </label>
          </>
        )}
        <label className="full-field">
          <span>{credentialLabel(credentialKind)}</span>
          <input
            aria-label="BYOK credential"
            autoComplete="new-password"
            disabled={busy || !deliveryReady}
            type="password"
            value={rawCredential}
            onChange={(event) => {
              setRawCredential(event.target.value);
              markDirty();
            }}
            placeholder={
              reusableBinding
                ? "Leave empty to reuse the secure binding"
                : "Sealed in this browser for the selected Computer"
            }
          />
          <small>
            The raw value is sealed in this browser. The Server only relays
            ciphertext and stores the returned non-secret binding handle.
          </small>
        </label>
        <fieldset className="runtime-tools full-field" disabled={busy}>
          <legend>Typed tools</legend>
          {!engineCapability?.supportsToolCalls ? (
            <small>
              This Engine currently declares no ToolGateway support.
            </small>
          ) : (
            <>
              <ToolToggle
                checked={messageTool}
                label="Message"
                onChange={setMessageTool}
              />
              <ToolToggle
                checked={workTool}
                label="Work"
                onChange={setWorkTool}
              />
              <ToolToggle
                checked={knowledgeTool}
                label="Knowledge"
                onChange={setKnowledgeTool}
              />
            </>
          )}
        </fieldset>
        {!deliveryReady && computer && (
          <p className="field-error full-field">
            Secure credential delivery is unavailable on this Computer. Runtime
            configuration fails closed.
          </p>
        )}
        {error && (
          <p className="field-error full-field" role="alert">
            {error}
          </p>
        )}
        {success && (
          <p className="field-success full-field" role="status">
            {success}
          </p>
        )}
        <div className="runtime-actions full-field">
          <button
            className="primary-action"
            disabled={busy || engines.length === 0 || !deliveryReady}
            type="submit"
          >
            {submissionText(submission)}
          </button>
        </div>
      </form>
    </section>
  );
}

function ToolToggle({
  checked,
  label,
  onChange,
}: {
  checked: boolean;
  label: string;
  onChange: (value: boolean) => void;
}) {
  return (
    <label className="runtime-tool-toggle">
      <input
        checked={checked}
        type="checkbox"
        onChange={(event) => onChange(event.target.checked)}
      />
      <span>{label}</span>
    </label>
  );
}

function credentialKindFor(engine: EngineKind, protocol: ProviderProtocol) {
  if (engine === EngineKind.CODEX_ADAPTER) return CredentialKind.CODEX_ADAPTER;
  if (engine === EngineKind.CLAUDE_ADAPTER)
    return CredentialKind.CLAUDE_ADAPTER;
  if (engine === EngineKind.BUILTIN) {
    if (protocol === ProviderProtocol.OPENAI_RESPONSES)
      return CredentialKind.OPENAI;
    if (protocol === ProviderProtocol.ANTHROPIC_MESSAGES)
      return CredentialKind.ANTHROPIC;
  }
  return CredentialKind.UNSPECIFIED;
}

function credentialKindName(kind: CredentialKind) {
  switch (kind) {
    case CredentialKind.OPENAI:
      return "openai";
    case CredentialKind.ANTHROPIC:
      return "anthropic";
    case CredentialKind.CODEX_ADAPTER:
      return "codex_adapter";
    case CredentialKind.CLAUDE_ADAPTER:
      return "claude_adapter";
    default:
      throw new Error("Credential kind is not configured.");
  }
}

function credentialLabel(kind: CredentialKind) {
  switch (kind) {
    case CredentialKind.OPENAI:
      return "OpenAI API key";
    case CredentialKind.ANTHROPIC:
      return "Anthropic API key";
    case CredentialKind.CODEX_ADAPTER:
      return "Codex credential";
    case CredentialKind.CLAUDE_ADAPTER:
      return "Claude credential";
    default:
      return "BYOK credential";
  }
}

function defaultEndpoint(protocol: ProviderProtocol) {
  if (protocol === ProviderProtocol.OPENAI_RESPONSES)
    return "https://api.openai.com";
  if (protocol === ProviderProtocol.ANTHROPIC_MESSAGES)
    return "https://api.anthropic.com";
  return "";
}

function providerProtocolText(protocol: ProviderProtocol) {
  if (protocol === ProviderProtocol.OPENAI_RESPONSES) return "OpenAI Responses";
  if (protocol === ProviderProtocol.ANTHROPIC_MESSAGES)
    return "Anthropic Messages";
  return "Unknown";
}

function credentialStateText(state: CredentialDeliveryState) {
  switch (state) {
    case CredentialDeliveryState.QUEUED:
      return "queued";
    case CredentialDeliveryState.CLAIMED:
      return "claimed";
    case CredentialDeliveryState.SUCCEEDED:
      return "succeeded";
    case CredentialDeliveryState.FAILED:
      return "failed";
    case CredentialDeliveryState.EXPIRED:
      return "expired";
    default:
      return "unknown";
  }
}

function submissionText(state: SubmissionState) {
  switch (state) {
    case "sealing":
      return "Sealing credential";
    case "delivering":
      return "Waiting for Computer";
    case "saving":
      return "Saving runtime";
    case "placing":
      return "Committing placement";
    case "succeeded":
      return "Runtime configured";
    default:
      return "Configure runtime";
  }
}

function placementStateText(placement: AgentPlacement) {
  if (placement.errorCode) return `failed (${placement.errorCode})`;
  return placement.state === 2 ? "ready" : "pending";
}
