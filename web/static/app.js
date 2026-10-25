"use strict";

const MAX_TEMP = 80;
let flightSheets = [];
let lastRigs = [];
let detailRigId = null;

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) throw new Error(await res.text());
  return res.status === 200 ? res.json() : null;
}

function el(tag, attrs = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (v !== undefined && v !== null) e.setAttribute(k, v);
  }
  for (const c of children.flat()) e.append(c instanceof Node ? c : String(c));
  return e;
}

const fmt = (n, d = 1) => Number(n || 0).toFixed(d);
const MINERS = { xmrig: "XMRig (CPU)", lolminer: "lolMiner (GPU)" };

// hashrate formats hashes per second with a readable unit.
function hashrate(hs) {
  const units = ["H/s", "kH/s", "MH/s", "GH/s", "TH/s"];
  let i = 0;
  hs = Number(hs || 0);
  while (hs >= 1000 && i < units.length - 1) {
    hs /= 1000;
    i++;
  }
  return `${hs.toFixed(hs < 10 && i > 0 ? 2 : 1)} ${units[i]}`;
}

function cpuCell(cpu, mining) {
  if (!cpu) return "—";
  const parts = [`${cpu.model || "Processeur"} · ${cpu.threads} threads`];
  if (cpu.temp_c) parts.push(`${fmt(cpu.temp_c, 0)} °C`);
  const lines = [el("div", {}, parts.join(" · "))];
  if (mining) {
    lines.push(el("div", {}, `${hashrate(cpu.hashrate_hs)} · ${cpu.mining_threads} threads de minage · huge pages ${fmt(cpu.huge_pages_pct, 0)} %`));
  }
  return lines;
}

function ago(ts) {
  if (!ts) return "jamais";
  const s = Math.round(Date.now() / 1000 - ts);
  if (s < 60) return `il y a ${s} s`;
  if (s < 3600) return `il y a ${Math.round(s / 60)} min`;
  return `il y a ${Math.round(s / 3600)} h`;
}

function sparkline(points) {
  const c = el("canvas", { class: "spark", width: 280, height: 64 });
  const values = points.map((p) => p.hashrate);
  if (values.length < 2) return c;
  const ctx = c.getContext("2d");
  const maxV = Math.max(...values) || 1;
  ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue("--accent");
  ctx.lineWidth = 3;
  ctx.beginPath();
  values.forEach((v, i) => {
    const x = (i / (values.length - 1)) * c.width;
    const y = c.height - 4 - (v / maxV) * (c.height - 8);
    i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
  });
  ctx.stroke();
  return c;
}

async function command(rig, type, label) {
  if (type === "reboot" && !confirm(`Redémarrer ${rig.name} ?`)) return;
  await api("POST", `/api/rigs/${rig.id}/commands`, { type });
  alert(`${label} envoyé à ${rig.name}. Il sera appliqué au prochain rapport.`);
}

async function renderRigs() {
  const rigs = await api("GET", "/api/rigs");
  lastRigs = rigs;
  const box = document.getElementById("rigs");

  const online = rigs.filter((r) => r.online);
  const total = online.reduce((s, r) => s + r.report.total_hashrate_hs, 0);
  const power = online.reduce((s, r) => s + (r.report.gpus || []).reduce((a, g) => a + g.power_w, 0), 0);
  document.getElementById("summary").textContent =
    `${online.length}/${rigs.length} rigs en ligne · ${hashrate(total)}` + (power ? ` · ${fmt(power, 0)} W` : "");

  if (!rigs.length) {
    box.replaceChildren(el("p", { class: "empty" }, "Aucun rig pour l'instant. Ajoute-en un ci-dessus."));
    return;
  }

  const histories = await Promise.all(rigs.map((r) => api("GET", `/api/rigs/${r.id}/history?hours=6`).catch(() => [])));

  const rows = rigs.map((rig, i) => {
    const gpus = rig.report.gpus || [];
    const cpu = rig.report.cpu;
    const hottest = Math.max(0, cpu ? cpu.temp_c : 0, ...gpus.map((g) => g.temp_c));
    const select = el("select", {
      onchange: async (e) => {
        const id = e.target.value ? Number(e.target.value) : null;
        await api("PUT", `/api/rigs/${rig.id}/flight-sheet`, { flight_sheet_id: id });
      },
    }, el("option", { value: "" }, "— aucune —"),
      flightSheets.map((fs) => el("option", { value: fs.id, ...(rig.flight_sheet_id === fs.id ? { selected: "" } : {}) }, fs.name)));

    return el("tr", {},
      el("td", {}, el("button", { class: "link", title: "Voir le détail", onclick: () => openDetail(rig.id) }, rig.name),
        el("div", { class: "muted" }, rig.report.hostname || rig.id)),
      el("td", {}, el("span", { class: `status ${rig.online ? "on" : "off"}` }, rig.online ? "En ligne" : "Hors ligne"),
        el("div", { class: "muted" }, ago(rig.last_seen)),
        rig.online && !rig.report.miner_running ? el("div", { class: "muted" }, "mineur arrêté") : ""),
      el("td", {}, hashrate(rig.report.total_hashrate_hs),
        rig.report.algo ? el("div", { class: "muted" }, `${rig.report.algo} · ${MINERS[rig.report.miner] || rig.report.miner}`) : "",
        sparkline(histories[i])),
      el("td", { class: hottest >= MAX_TEMP ? "hot" : "" }, hottest ? `${fmt(hottest, 0)} °C` : "—"),
      el("td", { class: "gpus" },
        cpuCell(cpu, rig.report.miner === "xmrig" && rig.report.miner_running),
        gpus.map((g) =>
          el("div", {}, `GPU #${g.index} ${g.name} · ${hashrate(g.hashrate_hs)} · ${fmt(g.temp_c, 0)} °C · ${fmt(g.fan_pct, 0)} % · ${fmt(g.power_w, 0)} W`))),
      el("td", {}, select),
      el("td", {}, el("div", { class: "row-actions" },
        el("button", { class: "secondary", onclick: () => command(rig, "restart_miner", "Redémarrage du mineur") }, "Relancer mineur"),
        el("button", { class: "secondary", onclick: () => command(rig, "stop_miner", "Arrêt du mineur") }, "Arrêter"),
        el("button", { class: "secondary", onclick: () => command(rig, "reboot", "Redémarrage") }, "Redémarrer"),
        el("button", {
          class: "danger",
          onclick: async () => {
            if (!confirm(`Supprimer ${rig.name} du tableau de bord ?`)) return;
            await api("DELETE", `/api/rigs/${rig.id}`);
            refresh();
          },
        }, "Supprimer"))),
    );
  });

  box.replaceChildren(el("div", { class: "table-wrap" }, el("table", {},
    el("thead", {}, el("tr", {}, ["Rig", "État", "Hashrate", "Temp. max", "Matériel", "Flight sheet", "Actions"].map((h) => el("th", {}, h)))),
    el("tbody", {}, rows))));
}

async function renderFlightSheets() {
  flightSheets = await api("GET", "/api/flight-sheets");
  const box = document.getElementById("flight-sheets");
  if (!flightSheets.length) {
    box.replaceChildren(el("p", { class: "empty" }, "Aucune flight sheet. Crée la première avec le formulaire."));
    return;
  }
  const form = document.getElementById("fs-form");
  box.replaceChildren(el("div", { class: "table-wrap" }, el("table", {},
    el("thead", {}, el("tr", {}, ["Nom", "Mineur", "Algo", "Pool", "Wallet", ""].map((h) => el("th", {}, h)))),
    el("tbody", {}, flightSheets.map((fs) => el("tr", {},
      el("td", {}, el("strong", {}, fs.name)),
      el("td", {}, MINERS[fs.miner] || fs.miner),
      el("td", {}, fs.algo),
      el("td", {}, fs.pool),
      el("td", { class: "muted" }, `${fs.wallet.slice(0, 10)}…${fs.worker ? "." + fs.worker : ""}`),
      el("td", {}, el("div", { class: "row-actions" },
        el("button", {
          class: "secondary",
          onclick: () => {
            for (const k of ["id", "name", "miner", "algo", "pool", "wallet", "worker", "extra_args"]) form.elements[k].value = fs[k] ?? "";
            form.scrollIntoView({ behavior: "smooth" });
          },
        }, "Modifier"),
        el("button", {
          class: "danger",
          onclick: async () => {
            if (!confirm(`Supprimer la flight sheet ${fs.name} ?`)) return;
            await api("DELETE", `/api/flight-sheets/${fs.id}`);
            refresh();
          },
        }, "Supprimer")))))))));
}

function duration(s) {
  if (!s) return "—";
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  if (d) return `${d} j ${h} h`;
  if (h) return `${h} h ${m} min`;
  return `${m} min`;
}

function card(value, label) {
  return el("div", { class: "card" }, el("div", { class: "value" }, value), el("div", { class: "label" }, label));
}

function kv(pairs) {
  return el("dl", { class: "kv" }, pairs.filter(([, v]) => v !== null && v !== undefined && v !== "")
    .flatMap(([k, v]) => [el("dt", {}, k), el("dd", {}, v)]));
}

function copyField(value) {
  const input = el("input", { readonly: "", value, onclick: (e) => e.target.select() });
  const btn = el("button", {
    class: "secondary",
    onclick: async () => {
      input.select();
      try {
        await navigator.clipboard.writeText(value);
      } catch {
        document.execCommand("copy");
      }
      btn.textContent = "Copié";
      setTimeout(() => (btn.textContent = "Copier"), 1500);
    },
  }, "Copier");
  return el("div", { class: "copy-row" }, input, btn);
}

// chart draws hashrate and max temperature over time, each on its own scale.
function chart(points) {
  const c = el("canvas", { class: "chart", width: 1800, height: 400 });
  if (points.length < 2) return c;
  const ctx = c.getContext("2d");
  const css = getComputedStyle(document.documentElement);
  const t0 = points[0].ts, t1 = points[points.length - 1].ts || t0 + 1;
  const pad = 16;
  const line = (key, color) => {
    const vals = points.map((p) => p[key]);
    const maxV = Math.max(...vals) * 1.1 || 1;
    ctx.strokeStyle = color;
    ctx.lineWidth = 4;
    ctx.beginPath();
    points.forEach((p, i) => {
      const x = pad + ((p.ts - t0) / (t1 - t0 || 1)) * (c.width - 2 * pad);
      const y = c.height - pad - (p[key] / maxV) * (c.height - 2 * pad);
      i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
    });
    ctx.stroke();
  };
  line("max_temp", css.getPropertyValue("--bad"));
  line("hashrate", css.getPropertyValue("--accent"));
  return c;
}

async function openDetail(id) {
  detailRigId = id;
  await renderDetail();
  const dlg = document.getElementById("rig-detail");
  if (!dlg.open) dlg.showModal();
}

async function renderDetail() {
  const dlg = document.getElementById("rig-detail");
  const rig = lastRigs.find((r) => r.id === detailRigId);
  if (!rig) {
    if (dlg.open) dlg.close();
    return;
  }
  const rep = rig.report;
  const cpu = rep.cpu;
  const gpus = rep.gpus || [];
  const fs = flightSheets.find((f) => f.id === rig.flight_sheet_id);
  const history = await api("GET", `/api/rigs/${rig.id}/history?hours=24`).catch(() => []);
  const avg = history.length ? history.reduce((s, p) => s + p.hashrate, 0) / history.length : 0;
  const hottest = Math.max(0, cpu ? cpu.temp_c : 0, ...gpus.map((g) => g.temp_c));
  const logBox = dlg.querySelector("pre.log");
  const logAtBottom = !logBox || logBox.scrollTop + logBox.clientHeight >= logBox.scrollHeight - 8;

  const mining = fs
    ? el("div", {},
      kv([
        ["Flight sheet", fs.name],
        ["Mineur", MINERS[fs.miner] || fs.miner],
        ["Algorithme", fs.algo],
        ["Pool", fs.pool],
        ["Worker", fs.worker || "—"],
        ["Arguments en plus", fs.extra_args],
      ]),
      el("h3", { style: "margin-top:12px" }, "Wallet"),
      copyField(fs.wallet))
    : el("p", { class: "muted" }, "Aucune flight sheet : ce rig ne mine pas.");

  const hardware = [];
  if (cpu) {
    hardware.push(kv([
      ["Processeur", cpu.model || "—"],
      ["Threads", `${cpu.threads}`],
      ["Température", cpu.temp_c ? `${fmt(cpu.temp_c, 0)} °C` : "non disponible"],
      ["Hashrate CPU", rep.miner === "xmrig" ? hashrate(cpu.hashrate_hs) : null],
      ["Threads de minage", rep.miner === "xmrig" ? `${cpu.mining_threads}` : null],
      ["Huge pages", rep.miner === "xmrig" ? `${fmt(cpu.huge_pages_pct, 0)} %` : null],
    ]));
  }
  for (const g of gpus) {
    hardware.push(el("h3", { style: "margin-top:12px" }, `GPU #${g.index} · ${g.name}`), kv([
      ["Hashrate", hashrate(g.hashrate_hs)],
      ["Température", `${fmt(g.temp_c, 0)} °C`],
      ["Ventilateur", `${fmt(g.fan_pct, 0)} %`],
      ["Consommation", `${fmt(g.power_w, 0)} W`],
      ["Fréquences", `${g.core_mhz} / ${g.mem_mhz} MHz`],
    ]));
  }

  dlg.replaceChildren(
    el("div", { class: "detail-head" },
      el("h2", {}, rig.name,
        el("span", { class: `status ${rig.online ? "on" : "off"}` }, rig.online ? "En ligne" : "Hors ligne"),
        el("span", { class: "muted", style: "font-size:14px;font-weight:400" }, `vu ${ago(rig.last_seen)}`)),
      el("div", { class: "row-actions" },
        el("button", { class: "secondary", onclick: () => command(rig, "restart_miner", "Redémarrage du mineur") }, "Relancer mineur"),
        el("button", { class: "secondary", onclick: () => command(rig, "stop_miner", "Arrêt du mineur") }, "Arrêter"),
        el("button", { class: "secondary", onclick: () => command(rig, "reboot", "Redémarrage") }, "Redémarrer"),
        el("button", { onclick: () => dlg.close() }, "Fermer"))),
    el("div", { class: "detail-body" },
      el("div", { class: "cards" },
        card(hashrate(rep.total_hashrate_hs), "Hashrate"),
        card(hashrate(avg), "Moyenne 24 h"),
        card(hottest ? `${fmt(hottest, 0)} °C` : "—", "Temp. max"),
        card(rep.miner_running ? "En cours" : "Arrêté", MINERS[rep.miner] || "Mineur"),
        card(duration(rep.system_uptime_s || rep.uptime_s), rep.system_uptime_s ? "Allumé depuis" : "Agent lancé depuis")),
      el("section", {},
        el("h3", {}, "Dernières 24 heures"),
        chart(history),
        el("div", { class: "legend" },
          el("span", { style: "--c: var(--accent)" }, "Hashrate"),
          el("span", { style: "--c: var(--bad)" }, "Température max"))),
      el("div", { class: "two-col" },
        el("section", {}, el("h3", {}, "Minage"), mining),
        el("section", {}, el("h3", {}, "Matériel"), hardware.length ? hardware : el("p", { class: "muted" }, "Pas encore de données."))),
      el("section", {},
        el("h3", {}, "Système"),
        kv([
          ["Nom de la machine", rep.hostname || "—"],
          ["Adresse IP", rep.ip || "—"],
          ["Agent", rep.agent_version ? `version ${rep.agent_version}, lancé depuis ${duration(rep.uptime_s)}` : "—"],
          ["Identifiant Ruche", rig.id],
        ])),
      el("section", {},
        el("h3", {}, "Journal du mineur"),
        (rep.miner_log || []).length
          ? el("pre", { class: "log" }, rep.miner_log.join("\n"))
          : el("p", { class: "muted" }, "Rien pour l'instant (agent 0.3 ou plus récent nécessaire)."))));

  const newLog = dlg.querySelector("pre.log");
  if (newLog && logAtBottom) newLog.scrollTop = newLog.scrollHeight;
}

async function refresh() {
  try {
    await renderFlightSheets();
    await renderRigs();
    if (detailRigId && document.getElementById("rig-detail").open) await renderDetail();
  } catch (e) {
    console.error(e);
  }
}

document.getElementById("add-rig").addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = e.target.elements.name.value.trim();
  const rig = await api("POST", "/api/rigs", { name });
  e.target.reset();
  const notice = document.getElementById("new-rig");
  notice.hidden = false;
  const conf = `server=${location.origin}\ntoken=${rig.token}\n`;
  const download = el("a", {
    class: "button",
    download: "rig.conf",
    href: URL.createObjectURL(new Blob([conf], { type: "text/plain" })),
  }, "Télécharger rig.conf");
  notice.replaceChildren(
    el("strong", {}, `Rig « ${rig.name} » créé.`),
    " Télécharge son fichier rig.conf et place-le à côté de ruche-agent.exe (Windows) ou dans /etc/ruche/ (Linux). Le token ne sera plus affiché.",
    el("pre", {}, conf),
    el("div", { class: "row-actions" }, download,
      el("button", { class: "secondary", onclick: () => (notice.hidden = true) }, "Fermer")),
  );
  refresh();
});

document.getElementById("fs-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(e.target));
  const id = data.id;
  delete data.id;
  await api(id ? "PUT" : "POST", id ? `/api/flight-sheets/${id}` : "/api/flight-sheets", data);
  e.target.reset();
  e.target.elements.id.value = "";
  e.target.elements.miner.value = "xmrig";
  refresh();
});

document.getElementById("rig-detail").addEventListener("close", () => (detailRigId = null));

refresh();
setInterval(refresh, 10000);
