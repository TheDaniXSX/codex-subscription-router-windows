const CODEX_MUX_USAGE_API = "http://127.0.0.1:__CODEX_MUX_CONTROL_PORT__/v1/usage";
const CODEX_MUX_USAGE_TOKEN = "__CODEX_MUX_CONTROL_TOKEN__";

function codexMuxUsageElement(type, props, ...children) {
  return React.createElement(type, props, ...children);
}

function codexMuxUsageApi(path, options = {}) {
  return fetch(`${CODEX_MUX_USAGE_API}${path}`, {
    ...options,
    headers: {
      "X-Codex-Mux-Token": CODEX_MUX_USAGE_TOKEN,
      ...(options.headers || {}),
    },
  }).then(async (response) => {
    const body = await response.json().catch(() => null);
    if (!response.ok) {
      const error = new Error(body?.error || `Request failed (${response.status})`);
      error.status = response.status;
      error.body = body;
      throw error;
    }
    return body;
  });
}

function codexMuxUsageTurnPath(threadId, turnId) {
  return `/turn?threadId=${encodeURIComponent(threadId)}&turnId=${encodeURIComponent(turnId)}`;
}

function codexMuxUsageCheckIdentity(view, threadId, turnId) {
  if (!view || typeof view !== "object") throw new Error("Turn usage response is unavailable");
  if ((view.threadId && view.threadId !== threadId) || (view.turnId && view.turnId !== turnId)) {
    throw new Error("Turn identity did not match the requested conversation");
  }
  return view;
}

function codexMuxUsageStoreView(setState, key, view) {
  setState((previous) => previous?.key === key && previous.view?.revision > view?.revision
    ? previous
    : { key, loading: false, view, error: "" });
}

function codexMuxUsageFormatPercent(value) {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  return `${Number(value.toPrecision(4))}%`;
}

function codexMuxUsageFormatUsd(value) {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  return `$${value !== 0 && Math.abs(value) < 0.0001 ? Number(value.toPrecision(4)) : value.toFixed(value !== 0 && Math.abs(value) < 0.01 ? 4 : 3)}`;
}

function codexMuxUsageFormatTokens(value) {
  return typeof value === "number" && Number.isFinite(value)
    ? new Intl.NumberFormat().format(value)
    : "No disponible";
}

function codexMuxUsageFormatTime(value) {
  if (typeof value !== "string" || value.length === 0 || value.startsWith("0001-")) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString();
}

function codexMuxUsageShortLabel(minutes) {
  if (minutes === 300) return "5 h";
  if (minutes === 10_080) return "7 d";
  return typeof minutes === "number" && Number.isFinite(minutes)
    ? `${minutes} min`
    : "Ventana";
}

function codexMuxUsageObservedValue(observation) {
  if (typeof observation?.observedPercentX20 === "number" && Number.isFinite(observation.observedPercentX20)) {
    return observation.observedPercentX20;
  }
  return null;
}

function codexMuxUsageNumber(value) {
  return typeof value === "number" && Number.isFinite(value) ? String(Number(value.toPrecision(6))) : "—";
}

function codexMuxUsageTokenSummary(usage) {
  return `Entrada total ${codexMuxUsageFormatTokens(usage?.inputTokens)} · caché leída ${codexMuxUsageFormatTokens(usage?.cachedInputTokens)} · caché escrita ${codexMuxUsageFormatTokens(usage?.cacheWriteTokens)} · salida ${codexMuxUsageFormatTokens(usage?.outputTokens)} · razonamiento ${codexMuxUsageFormatTokens(usage?.reasoningTokens)} (incluido en salida)`;
}

function codexMuxUsageRateSummary(request) {
  const rate = request.apiCost?.rateCard;
  if (!rate) return "Tarifas API no disponibles";
  const input = request.usage?.inputTokens;
  const long = typeof input === "number" && (
    (typeof rate.longContextThreshold === "number" && input > rate.longContextThreshold) ||
    (typeof rate.shortContextMaxTokens === "number" && input > rate.shortContextMaxTokens)
  );
  const prices = long
    ? [rate.longInputUsdPerMillion, rate.longCachedUsdPerMillion, rate.longCacheWriteUsdPerMillion, rate.longOutputUsdPerMillion]
    : [rate.inputUsdPerMillion, rate.cachedInputUsdPerMillion, rate.cacheWriteUsdPerMillion, rate.outputUsdPerMillion];
  return `Tarifas API USD / millón (${rate.apiTier || "tier desconocido"}, contexto ${long ? "largo" : "corto"}): entrada ${codexMuxUsageNumber(prices[0])} · caché leída ${codexMuxUsageNumber(prices[1])} · caché escrita ${codexMuxUsageNumber(prices[2])} · salida ${codexMuxUsageNumber(prices[3])}`;
}

function codexMuxUsageEstimateSummary(label, estimate) {
  const features = estimate?.featureCoefficients || [];
  return `${label}: ${codexMuxUsageFormatPercent(estimate?.percentX20)} · ${estimate?.sampleCount ?? 0} muestras · ${estimate?.confidence || "sin calibración"} · versión ${estimate?.modelVersion || "—"} · coeficiente ${codexMuxUsageNumber(estimate?.coefficient)} pp Pro ×20 / USD relativo${features.length ? ` · coeficientes entrada/caché/salida ${features.map(codexMuxUsageNumber).join(" / ")}` : ""} · factor Fast supuesto ${codexMuxUsageNumber(estimate?.fastMultiplier)} · capacidad nativa ×${codexMuxUsageNumber(estimate?.planMultiplier)} (auditoría; la predicción ya es ×20)${estimate?.unavailableReason ? ` · ${estimate.unavailableReason}` : ""}`;
}

function codexMuxUsageSnapshotSummary(snapshot) {
  return `${codexMuxUsageFormatTime(snapshot?.observedAt)}: ${codexMuxUsageFormatPercent(snapshot?.usedPercent)} · fuente ${snapshot?.source || "—"} · calidad ${snapshot?.quality || "—"}${typeof snapshot?.resetsAt === "number" ? ` · reinicio ${codexMuxUsageFormatTime(new Date(snapshot.resetsAt * 1000).toISOString())}` : ""}`;
}

function codexMuxUsageBucketSummary(view, bucket, estimateField) {
  if (!view) return { text: "—", shared: false, partial: false, count: 0 };
  if (estimateField) {
    const value = view[estimateField]?.percentX20;
    return {
      text: codexMuxUsageFormatPercent(value),
      shared: false,
      partial: value == null,
      count: value == null ? 0 : 1,
    };
  }
  const unique = new Map();
  for (const [index, observation] of (view.observations || []).entries()) {
    if (observation.bucket !== bucket) continue;
    const identity = observation.id || `unidentified-${index}`;
    if (!unique.has(identity)) unique.set(identity, observation);
  }
  const matches = Array.from(unique.values());
  const shared = matches.some((item) => (item.rootTurnIds || []).length > 1);
  const values = matches.map(codexMuxUsageObservedValue).filter((value) => value != null);
  const partial = values.length < matches.length;
  const value = values.length === 0 ? null : values.reduce((total, next) => total + next, 0);
  const text = value == null
    ? "—"
    : `${codexMuxUsageFormatPercent(value)}${partial ? " parcial" : ""}${shared ? "*" : ""}`;
  return { text, shared, partial, count: matches.length, value };
}

function codexMuxUsageIcon(kind) {
  const paths = {
    calibration: "M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4m5.6 5.6 1.4 1.4m0-8.4-1.4 1.4m-5.6 5.6-1.4 1.4M8 4.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7Zm-1.4 3.5 1 1 2-2.2",
    observed: "M1.5 8h2l1.5-4 2.2 8 2-6 1.2 2h2.1",
    estimated: "M3 2h10v12H3zM5 5h2m2 0h2M5 8h2m2 0h2M5 11h2m2 0h2",
    api: "M8 1.5v13M11 4.2c-.6-.8-1.5-1.2-2.8-1.2-1.6 0-2.6.8-2.6 2s1 1.8 3.1 2.2c2.1.4 3.2 1 3.2 2.4s-1.3 2.4-3.1 2.4c-1.3 0-2.4-.5-3.1-1.4",
  };
  return codexMuxUsageElement(
    "svg",
    {
      "aria-hidden": true,
      className: "size-3.5 shrink-0",
      fill: "none",
      viewBox: "0 0 16 16",
    },
    codexMuxUsageElement("path", {
      d: paths[kind],
      stroke: "currentColor",
      strokeLinecap: "round",
      strokeLinejoin: "round",
      strokeWidth: 1.25,
    }),
  );
}

function codexMuxUsageMetricButton({ action, icon, label, title, expanded, panelId, onClick, disabled }) {
  return codexMuxUsageElement(
    "button",
    {
      type: "button",
      className: "inline-flex max-w-full items-center gap-1 rounded px-1.5 py-0.5 text-xs text-token-description-foreground hover:bg-token-surface-hover hover:text-token-text-primary focus-visible:outline focus-visible:outline-1",
      "data-codex-mux-usage-action": action,
      "aria-label": title,
      "aria-expanded": expanded,
      "aria-controls": panelId,
      title,
      disabled: disabled === true,
      onClick,
    },
    codexMuxUsageIcon(icon),
    codexMuxUsageElement("span", { className: "truncate tabular-nums" }, label),
  );
}

function CodexMuxTurnUsage({ threadId, turnId }) {
  const key = threadId && turnId ? `${threadId}\u0000${turnId}` : "";
  const [turnState, setTurnState] = React.useState({ key: "", loading: false, view: null, error: "" });
  const [expanded, setExpanded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [notice, setNotice] = React.useState("");
  const [statusState, setStatusState] = React.useState({ key: "", data: null, loaded: false });
  const sessionRef = React.useRef(null);
  if (sessionRef.current?.key !== key) {
    sessionRef.current = { key, active: true, controllers: new Set(), refreshing: null };
  }
  const session = sessionRef.current;
  const isCurrent = () => session.active && sessionRef.current === session;
  const request = async (path, options = {}) => {
    const controller = new AbortController();
    session.controllers.add(controller);
    try {
      return await codexMuxUsageApi(path, { ...options, signal: controller.signal });
    } finally {
      session.controllers.delete(controller);
    }
  };
  const refreshTurn = () => {
    if (!isCurrent()) return Promise.resolve();
    if (session.refreshing) return session.refreshing;
    session.refreshing = request(codexMuxUsageTurnPath(threadId, turnId))
      .then((body) => {
        if (!isCurrent()) return;
        codexMuxUsageStoreView(setTurnState, key, codexMuxUsageCheckIdentity(body, threadId, turnId));
      })
      .catch((requestError) => {
        if (!isCurrent() || requestError?.name === "AbortError") return;
        setTurnState((previous) => previous?.key === key && previous.view
          ? { ...previous, loading: false, error: "No se pudo actualizar el consumo" }
          : { key, loading: false, view: null, error: "Consumo no disponible" });
      })
      .finally(() => { session.refreshing = null; });
    return session.refreshing;
  };

  React.useEffect(() => {
    if (!threadId || !turnId) return undefined;
    let active = true;
    let attempts = 0;
    let timer = null;
    session.active = true;
    setTurnState({ key, loading: true, view: null, error: "" });
    setExpanded(false);
    setSaving(false);
    setNotice("");
    setStatusState({ key, data: null, loaded: false });
    const refresh = () => {
      if (!active) return;
      attempts += 1;
      refreshTurn()
        .finally(() => {
          if (active && attempts < 16) timer = setTimeout(refresh, 2_000);
        });
    };
    refresh();
    return () => {
      active = false;
      session.active = false;
      clearTimeout(timer);
      for (const controller of session.controllers) controller.abort();
    };
  }, [threadId, turnId]);

  const current = turnState.key === key ? turnState : null;
  const view = current?.view || null;
  const loading = current?.loading === true;
  const error = current?.error || "";
  const shortObserved = codexMuxUsageBucketSummary(view, "short", null);
  const weeklyObserved = codexMuxUsageBucketSummary(view, "weekly", null);
  const shortEstimated = codexMuxUsageBucketSummary(view, "short", "estimatedShort");
  const weeklyEstimated = codexMuxUsageBucketSummary(view, "weekly", "estimatedWeekly");
  const totalApiUsd = typeof view?.totalApiCostUsd === "number" ? view.totalApiCostUsd : null;
  const knownApiUsd = typeof view?.totalKnownApiCostUsd === "number" ? view.totalKnownApiCostUsd : null;
  const apiLabel = totalApiUsd == null && knownApiUsd != null
    ? `≥${codexMuxUsageFormatUsd(knownApiUsd)} parcial`
    : codexMuxUsageFormatUsd(totalApiUsd);
  const panelId = `codex-mux-turn-usage-${encodeURIComponent(threadId)}-${encodeURIComponent(turnId)}`;

  React.useEffect(() => {
    if (!expanded || !threadId || !turnId) return undefined;
    let active = true;
    let timer = null;
    const poll = () => {
      if (!active) return;
      refreshTurn()
        .finally(() => {
          if (active) timer = setTimeout(poll, 2_000);
        });
    };
    timer = setTimeout(poll, 2_000);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [expanded, threadId, turnId]);

  const refreshStatus = () => request("/status")
    .then((status) => { if (isCurrent()) setStatusState({ key, data: status, loaded: true }); })
    .catch(() => { if (isCurrent()) setStatusState({ key, data: null, loaded: true }); });

  const toggleDetails = () => {
    const next = !expanded;
    setExpanded(next);
    if (!next) return;
    void refreshTurn();
    if (statusState.key !== key || !statusState.loaded) void refreshStatus();
  };

  const setCalibration = async (included, includeRelated = false) => {
    if (!view?.rootTurnId || saving || !isCurrent()) return;
    setSaving(true);
    setNotice("");
    try {
      const updated = await request("/calibration", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          rootId: view.rootTurnId,
          included,
          revision: view.revision,
          ...(includeRelated ? { includeRelated: true } : {}),
        }),
      });
      if (!isCurrent()) return;
      codexMuxUsageStoreView(setTurnState, key, codexMuxUsageCheckIdentity(updated, threadId, turnId));
      setNotice(included ? "Selección guardada" : "Muestra excluida");
      setStatusState({ key, data: null, loaded: false });
      void refreshStatus();
    } catch (updateError) {
      if (!isCurrent()) return;
      if (updateError?.status === 409) {
        try {
          const latest = codexMuxUsageCheckIdentity(
            await request(codexMuxUsageTurnPath(threadId, turnId)),
            threadId,
            turnId,
          );
          if (!isCurrent()) return;
          codexMuxUsageStoreView(setTurnState, key, latest);
          setNotice("El turno cambió; se cargó su estado actual.");
        } catch {
          if (isCurrent()) setNotice("No se pudo actualizar. Vuelve a intentarlo.");
        }
      } else {
        setNotice("No se pudo guardar la selección.");
      }
    } finally {
      if (isCurrent()) setSaving(false);
    }
  };

  const calibrationEnabled = view?.calibrationEnabled === true;
  const calibrationTitle = calibrationEnabled
    ? "Excluir este turno de la calibración"
    : "Usar este turno para calibrar; confirma que no hubo consumo externo desconocido";
  const status = statusState.key === key ? statusState.data : null;
  const hasPartialCoverage = view != null && (
    view.associationComplete === false ||
    (view.pendingRelations || 0) > 0 ||
    (view.activeDescendants || 0) > 0 ||
    view.totalUsageComplete !== true
  );

  const observationRows = (view?.observations || []).map((observation) => {
    const observed = codexMuxUsageObservedValue(observation);
    const before = observation.before?.usedPercent;
    const after = observation.after?.usedPercent;
    const multiRoot = (observation.rootTurnIds || []).length > 1;
    const sharedMarker = multiRoot ? "*" : "";
    const groupButton = multiRoot && observation.calibrationEnabled !== true
      ? codexMuxUsageElement("button", {
          type: "button",
          className: "mt-1 rounded px-2 py-1 text-xs text-token-text-primary underline focus-visible:outline focus-visible:outline-1",
          "data-codex-mux-usage-action": "include-group",
          "aria-label": "Incluir todos los turnos relacionados de los grupos compartidos en la calibración",
          disabled: saving,
          onClick: () => setCalibration(true, true),
          children: `Incluir todos los turnos relacionados (este grupo: ${(observation.rootTurnIds || []).length})`,
        })
      : null;
    return codexMuxUsageElement("li", {
      key: observation.id,
      className: "rounded border border-token-border-default p-2",
      "data-codex-mux-usage-observation": observation.id,
    },
      codexMuxUsageElement("div", { className: "font-medium" }, `${observation.bucket === "short" ? "Ventana corta" : observation.bucket === "weekly" ? "Ventana semanal" : observation.bucket}: ${codexMuxUsageFormatPercent(observed)}${sharedMarker}`),
      codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Pro20 equivalente · ${observation.plan || "plan desconocido"} · cuenta ${observation.accountId || "desconocida"}`),
      codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Cuota nativa: ${codexMuxUsageFormatPercent(before)} → ${codexMuxUsageFormatPercent(after)} · variación ${codexMuxUsageNumber(observation.rawDeltaPp)} pp · ${codexMuxUsageShortLabel(observation.before?.windowDurationMins)} · ${observation.eligible ? "elegible" : observation.eligibilityReason || "pendiente"}`),
      codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, observation.capacityMultiplier > 0
        ? `Normalización Pro ×20: ${codexMuxUsageNumber(observation.rawDeltaPp)} pp × ${codexMuxUsageNumber(observation.capacityMultiplier)} / 20 = ${codexMuxUsageFormatPercent(observed)}`
        : "Normalización Pro ×20 no disponible: capacidad de la cuenta desconocida"),
      codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Lecturas: ${codexMuxUsageFormatTime(observation.before?.observedAt)} → ${codexMuxUsageFormatTime(observation.after?.observedAt)} · solicitudes: ${(observation.requestIds || []).join(", ") || "sin asociar"} · turnos: ${(observation.rootTurnIds || []).join(", ") || "sin asociar"}`),
      codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Inicial: ${codexMuxUsageSnapshotSummary(observation.before)}`),
      codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Final: ${codexMuxUsageSnapshotSummary(observation.after)}`),
      observation.samples?.length ? codexMuxUsageElement("ul", { className: "mt-1 space-y-0.5", "aria-label": "Lecturas posteriores de cuota" }, observation.samples.map((sample, index) => codexMuxUsageElement("li", { key: index }, codexMuxUsageSnapshotSummary(sample)))) : null,
      multiRoot ? codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, "* Esta observación mide un grupo compartido; no representa el consumo individual de cada turno.") : null,
      groupButton,
    );
  });

  const records = view?.requests || [];
  const requestGroups = new Map();
  for (const request of records) {
    const groupId = request.agentId || (request.threadId && request.threadId !== view.threadId ? request.threadId : "__root__");
    if (!requestGroups.has(groupId)) requestGroups.set(groupId, []);
    requestGroups.get(groupId).push(request);
  }
  const requestGroupsByAgent = Array.from(requestGroups, ([agentId, requests]) => {
    const byId = new Map(requests.map((request) => [request.requestId, request]));
    const visited = new Set();
    const renderRequest = (request, depth = 0) => {
      if (!request?.requestId || visited.has(request.requestId)) return null;
      visited.add(request.requestId);
      const children = requests.filter((candidate) => candidate.parentRequestId === request.requestId);
      const externalParent = request.parentRequestId && !byId.has(request.parentRequestId)
        ? ` · continúa desde ${request.parentRequestId}` : "";
      const fast = request.fast === true ? "Fast activado" : request.fast === false ? "Fast desactivado" : "Fast desconocido";
      const rateVersion = request.apiCost?.rateVersion || "tarifa sin versión";
      const requestObservations = (view?.observations || []).filter((item) => (item.requestIds || []).includes(request.requestId));
      return codexMuxUsageElement("li", {
        key: request.requestId,
        className: "rounded border border-token-border-default p-2",
        style: { marginInlineStart: `${Math.min(depth, 8) * 12}px` },
        "data-codex-mux-usage-request": request.requestId,
        "data-parent-request-id": request.parentRequestId || undefined,
      },
        codexMuxUsageElement("div", { className: "font-medium" }, `${request.modelServed || request.modelRequested || "Modelo desconocido"}${request.subagent ? " · subagente" : ""} · ${request.outcome || "estado desconocido"}`),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `${request.tierServed || request.tierRequested || "Tier desconocido"} · ${fast}${request.reasoningEffort ? ` · esfuerzo ${request.reasoningEffort}` : ""}${request.accountPlan ? ` · ${request.accountPlan}` : ""} · ${request.accountId || "cuenta desconocida"} · agente ${request.agentId || "raíz"}${externalParent}`),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Chat ${request.threadId || "—"} · turno ${request.turnId || "—"} · chat padre ${request.parentThreadId || "—"} · operación ${request.requestId}`),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Inicio: ${codexMuxUsageFormatTime(request.startedAt)} · fin: ${codexMuxUsageFormatTime(request.finishedAt)}`),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, codexMuxUsageTokenSummary(request.usage)),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Uso: fuente ${request.usage?.source || "—"} · calidad ${request.usage?.quality || "—"}`),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, codexMuxUsageRateSummary(request)),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Coste API por categoría: entrada ${codexMuxUsageFormatUsd(request.apiCost?.inputUsd)} · caché leída ${codexMuxUsageFormatUsd(request.apiCost?.cachedInputUsd)} · caché escrita ${codexMuxUsageFormatUsd(request.apiCost?.cacheWriteUsd)} · salida ${codexMuxUsageFormatUsd(request.apiCost?.outputUsd)} · total ${codexMuxUsageFormatUsd(request.apiCost?.usd)} · tarifa ${rateVersion}`),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, "API = suma de tokens por categoría × tarifa / 1.000.000; entrada ordinaria = entrada total − caché leída − caché escrita."),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, codexMuxUsageRateSummary({ ...request, apiCost: { rateCard: request.apiCost?.standardRateCard } }).replace("Tarifas API", "Pesos iniciales de cuota según API estándar")),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, "Estimación = suma por categoría de (tokens × peso API estándar / 1.000.000 × factor Fast supuesto × coeficiente calibrado). Para cuota, la entrada no cacheada incluye la escritura de caché."),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, `Estimación Pro ×20 congelada al completar. ${codexMuxUsageEstimateSummary("Corta", request.estimatedShort)}`),
        codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, codexMuxUsageEstimateSummary("Semanal", request.estimatedWeekly)),
        requestObservations.length ? codexMuxUsageElement("ul", { className: "mt-1 space-y-0.5", "aria-label": "Observaciones que incluyen esta operación" }, requestObservations.map((observation) => codexMuxUsageElement("li", { key: observation.id }, `Medición ${observation.id} (${observation.bucket}): ${codexMuxUsageFormatPercent(codexMuxUsageObservedValue(observation))}${observation.rootTurnIds?.length > 1 ? "*" : ""} · ${codexMuxUsageFormatPercent(observation.before?.usedPercent)} → ${codexMuxUsageFormatPercent(observation.after?.usedPercent)} · ${codexMuxUsageFormatTime(observation.before?.observedAt)} → ${codexMuxUsageFormatTime(observation.after?.observedAt)} · ${observation.requestIds?.length || 0} operaciones medidas juntas`))) : null,
        request.apiCost?.unavailableReason || request.estimatedShort?.unavailableReason || request.estimatedWeekly?.unavailableReason
          ? codexMuxUsageElement("div", { className: "text-xs text-token-description-foreground" }, request.apiCost?.unavailableReason || request.estimatedShort?.unavailableReason || request.estimatedWeekly?.unavailableReason)
          : null,
        children.length ? codexMuxUsageElement("ul", { className: "mt-1 space-y-1", "aria-label": `Continuaciones de ${request.requestId}` }, children.map((child) => renderRequest(child, depth + 1))) : null,
      );
    };
    const roots = requests.filter((request) => !byId.has(request.parentRequestId));
    const nodes = roots.map((request) => renderRequest(request));
    for (const request of requests) {
      if (!visited.has(request.requestId)) nodes.push(renderRequest(request));
    }
    const summary = (view?.agents || []).find((agent) => (agent.agentId || "__root__") === agentId);
    return codexMuxUsageElement("details", {
      key: agentId,
      className: "mb-2",
      role: "group",
      "aria-label": agentId === "__root__" ? "Inferencias del turno raíz" : `Agente ${agentId}`,
      "data-codex-mux-usage-agent": agentId,
    },
      codexMuxUsageElement("summary", { className: "mb-1 cursor-pointer font-medium focus-visible:outline focus-visible:outline-1" }, `${agentId === "__root__" ? "Turno raíz" : `Agente ${agentId}`} · ${summary?.requestCount ?? requests.length} inferencias · API ${codexMuxUsageFormatUsd(summary?.apiCostUsd)}`),
      summary?.usage ? codexMuxUsageElement("div", { className: "mb-1 text-xs text-token-description-foreground" }, codexMuxUsageTokenSummary(summary.usage)) : null,
      codexMuxUsageElement("ul", { className: "space-y-1" }, nodes),
    );
  });

  const sampleRows = (status?.buckets || []).map((bucket) => codexMuxUsageElement("span", {
    key: bucket.bucket,
    className: "rounded bg-token-surface-secondary px-1.5 py-0.5",
  }, `${bucket.bucket === "short" ? "Corta" : bucket.bucket === "weekly" ? "Semanal" : bucket.bucket}: ${bucket.sampleCount} muestras · ${bucket.confidence || "confianza no disponible"}`));

  const children = [
    codexMuxUsageElement("button", {
      type: "button",
      className: `inline-flex max-w-full items-center gap-1 rounded px-1.5 py-0.5 text-xs focus-visible:outline focus-visible:outline-1 ${calibrationEnabled ? "text-token-text-primary" : "text-token-description-foreground"}`,
      "data-codex-mux-usage-action": "calibration",
      "aria-label": calibrationTitle,
      "aria-pressed": calibrationEnabled,
      title: calibrationTitle,
      disabled: saving || !view?.rootTurnId,
      onClick: () => setCalibration(!calibrationEnabled),
    }, codexMuxUsageIcon("calibration"), codexMuxUsageElement("span", {}, calibrationEnabled ? "Incluida" : "Calibrar")),
    codexMuxUsageMetricButton({
      action: "observed",
      icon: "observed",
      label: `Obs ${shortObserved.text} / ${weeklyObserved.text}`,
      title: `Consumo observado normalizado a Pro20; ventana corta: ${shortObserved.text}; semanal: ${weeklyObserved.text}. Los grupos del mismo bucket se suman una sola vez por ID; * identifica grupos compartidos.`,
      expanded,
      panelId,
      onClick: toggleDetails,
      disabled: false,
    }),
    codexMuxUsageMetricButton({
      action: "estimated",
      icon: "estimated",
      label: `Est ${shortEstimated.text} / ${weeklyEstimated.text}`,
      title: `Estimación normalizada a Pro20; ventana corta: ${shortEstimated.text}; semanal: ${weeklyEstimated.text}.`,
      expanded,
      panelId,
      onClick: toggleDetails,
      disabled: false,
    }),
    codexMuxUsageMetricButton({
      action: "api-cost",
      icon: "api",
      label: `API ${apiLabel}`,
      title: totalApiUsd == null ? (knownApiUsd == null ? "Coste API no disponible" : `Coste API parcial: al menos ${codexMuxUsageFormatUsd(knownApiUsd)}; faltan categorías por medir`) : `Coste API equivalente calculado: ${codexMuxUsageFormatUsd(totalApiUsd)}`,
      expanded,
      panelId,
      onClick: toggleDetails,
      disabled: false,
    }),
    codexMuxUsageElement("span", {
      className: "sr-only",
      role: "status",
      "aria-live": "polite",
      "data-codex-mux-usage-feedback": true,
    }, notice),
  ];

  if (expanded) {
    children.push(codexMuxUsageElement("div", {
      id: panelId,
      role: "region",
      "aria-label": "Detalle del consumo de este turno",
      className: "basis-full min-w-0 rounded-md border border-token-border-default bg-token-surface-primary p-2 text-xs text-token-text-primary",
      "data-codex-mux-usage-details": true,
    },
      loading ? codexMuxUsageElement("div", { "aria-live": "polite" }, "Cargando consumo…") : null,
      error ? codexMuxUsageElement("div", { role: "status", "aria-live": "polite" }, error) : null,
      view ? codexMuxUsageElement("div", { className: "mb-2 space-y-1" },
        codexMuxUsageElement("div", {}, `Turno raíz ${view.rootTurnId} · ${view.requests?.length || 0} inferencias · ${hasPartialCoverage ? "cobertura parcial o pendiente" : "cobertura completa"} · asociaciones: ${view.associationComplete === false ? "incompletas" : "completas"} · relaciones pendientes: ${view.pendingRelations || 0} · descendientes activos: ${view.activeDescendants || 0}`),
        codexMuxUsageElement("div", {}, `${codexMuxUsageTokenSummary(view.totalUsage)} · API ${apiLabel}`),
        codexMuxUsageElement("div", {}, `Estimación Pro20 — corta: ${codexMuxUsageFormatPercent(view.estimatedShort?.percentX20)} (${view.estimatedShort?.confidence || "confianza no disponible"}, ${view.estimatedShort?.modelVersion || "sin versión"}); semanal: ${codexMuxUsageFormatPercent(view.estimatedWeekly?.percentX20)} (${view.estimatedWeekly?.confidence || "confianza no disponible"}, ${view.estimatedWeekly?.modelVersion || "sin versión"})`),
        sampleRows.length ? codexMuxUsageElement("div", { className: "flex flex-wrap gap-1" }, sampleRows) : null,
        notice ? codexMuxUsageElement("div", { role: "status", "aria-live": "polite" }, notice) : null,
      ) : null,
      observationRows.length ? codexMuxUsageElement("section", { className: "mb-2" },
        codexMuxUsageElement("h4", { className: "mb-1 font-medium" }, "Mediciones de cuota; buckets separados"),
        (shortObserved.shared || weeklyObserved.shared) ? codexMuxUsageElement("div", { className: "mb-1 text-xs text-token-description-foreground" }, "* El valor observado corresponde a un grupo compartido y no se puede atribuir a un turno individual.") : null,
        codexMuxUsageElement("ul", { className: "space-y-1" }, observationRows),
      ) : codexMuxUsageElement("div", { className: "mb-2" }, "Observación de cuota pendiente o no disponible; no equivale a 0%."),
      requestGroupsByAgent.length ? codexMuxUsageElement("section", {},
        codexMuxUsageElement("h4", { className: "mb-1 font-medium" }, "Inferencias, continuaciones y subagentes"),
        requestGroupsByAgent,
      ) : null,
    ));
  }

  if (!threadId || !turnId) return null;
  return codexMuxUsageElement("div", {
    className: "inline-flex min-w-0 max-w-full flex-wrap items-center gap-0.5 align-middle",
    "data-codex-mux-turn-usage": key,
    "aria-busy": loading || saving,
  }, children);
}
