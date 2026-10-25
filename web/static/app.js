"use strict";

const MAX_TEMP = 80;
let flightSheets = [];

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
      el("td", {}, el("strong", {}, rig.name), el("div", { class: "muted" }, rig.report.hostname || rig.id)),
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

async function refresh() {
  try {
    await renderFlightSheets();
    await renderRigs();
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

refresh();
setInterval(refresh, 10000);
