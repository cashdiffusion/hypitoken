// Offline, self-contained HYPITOKEN token-card renderer. Ported from
// design/token-cards/index.html with every CDN dependency removed: fonts fall
// back to bundled/system families, the QR is generated offline via
// qrcode-generator, and the design's WebGL light-flow is reproduced with a
// React-Three-Fiber 3D scene. Both faces (front + back) render stacked so
// the whole card is visible at once. The faces are single SVG strings so they
// export cleanly to PNG/SVG (the share artifact that drives referrals).

import { Canvas, useFrame } from "@react-three/fiber";
import qrcode from "qrcode-generator";
import { useId, useMemo, useRef } from "react";
import * as THREE from "three";

export type CardStyle = "openai" | "claude";
export type CardTone = "dark" | "light";

export interface TokenCardProps {
  style: CardStyle;
  tone: CardTone;
  /** Headline value, e.g. "$1 + $1" for an invite or "$25" for a gift. */
  value: string;
  /** Tier label shown in the corner badge (NOIR / PLATINUM / …). */
  tier?: string;
  /** Custom tagline (one or two lines, split on newline). */
  tagline?: string;
  /** Short custom message / sub-line. */
  message?: string;
  /** The card number / invite code / redeem code shown across the face. */
  code: string;
  /** Absolute URL encoded into the QR + linked from the card. */
  redeemUrl?: string;
  /** Caption under the value, e.g. "邀请奖励" or "礼品卡". */
  caption?: string;
  /** Serial / handle line. */
  serial?: string;
}

interface Theme {
  bg: [string, string, string];
  glow: string;
  glow2: string;
  fg: string;
  muted: string;
  hair: string;
  chipA: string;
  chipB: string;
  chipEdge: string;
  foil: string;
  accent: string;
  accent2: string;
  guilloche: string;
}

// Ported palette from the design's THEMES map.
const THEMES: Record<string, Theme> = {
  "openai-dark": {
    bg: ["#0a0b0f", "#14161d", "#0c0d12"],
    glow: "#2e6bff",
    glow2: "#10a37f",
    fg: "#f4f6fa",
    muted: "#8b93a3",
    hair: "rgba(255,255,255,.10)",
    chipA: "#dfe4ea",
    chipB: "#9aa3b2",
    chipEdge: "#c3cad4",
    foil: "#aeb6c4",
    accent: "#5b8cff",
    accent2: "#15c79a",
    guilloche: "rgba(120,150,220,.16)",
  },
  "openai-light": {
    bg: ["#f6f6f1", "#ecede7", "#e3e4dd"],
    glow: "#1a66ff",
    glow2: "#0e8c6f",
    fg: "#0d0e12",
    muted: "#6b6f78",
    hair: "rgba(13,14,18,.10)",
    chipA: "#e8eaed",
    chipB: "#aab1bd",
    chipEdge: "#bcc2cb",
    foil: "#9aa1ab",
    accent: "#1a55ff",
    accent2: "#0e8c6f",
    guilloche: "rgba(40,70,150,.14)",
  },
  "claude-dark": {
    bg: ["#1b1310", "#2a1c15", "#15110e"],
    glow: "#d97757",
    glow2: "#6a9bcc",
    fg: "#faf9f5",
    muted: "#b3a99c",
    hair: "rgba(255,243,234,.12)",
    chipA: "#f0d9a8",
    chipB: "#c79a4e",
    chipEdge: "#d9a441",
    foil: "#caa15a",
    accent: "#e89070",
    accent2: "#7fa8d4",
    guilloche: "rgba(217,119,87,.20)",
  },
  "claude-light": {
    bg: ["#faf8f2", "#f1ede2", "#e7dccb"],
    glow: "#d97757",
    glow2: "#c15f3c",
    fg: "#1a1714",
    muted: "#7a7367",
    hair: "rgba(26,23,20,.10)",
    chipA: "#f3dca6",
    chipB: "#cfa14e",
    chipEdge: "#c79a4e",
    foil: "#b88a3f",
    accent: "#c15f3c",
    accent2: "#788c5d",
    guilloche: "rgba(193,95,60,.18)",
  },
};

const W = 1012;
const H = 638;
const R = 52;

const esc = (s: string) =>
  String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

const SANS = "Bricolage Grotesque, ui-sans-serif, system-ui, sans-serif";
const SERIF = "Fraunces, Georgia, ui-serif, serif";
const MONO = "JetBrains Mono, ui-monospace, monospace";

function guilloche(cx: number, cy: number, color: string): string {
  let p = `<g opacity=".85">`;
  const N = 46;
  for (let i = 0; i < N; i++) {
    const a = (i / N) * Math.PI * 2;
    const rx = 180 + 36 * Math.sin(i * 0.7);
    const ry = 60 + 28 * Math.cos(i * 0.9);
    p += `<ellipse cx="${cx}" cy="${cy}" rx="${rx.toFixed(1)}" ry="${ry.toFixed(1)}" fill="none" stroke="${color}" stroke-width="0.6" transform="rotate(${((a * 180) / Math.PI).toFixed(2)} ${cx} ${cy})"/>`;
  }
  return `${p}</g>`;
}

function chip(x: number, y: number, t: Theme, sid: string): string {
  const w = 92;
  const h = 70;
  const r = 12;
  const X = (q: number) => (w * q).toFixed(1);
  const Y = (q: number) => (h * q).toFixed(1);
  return `
  <g transform="translate(${x},${y})">
    <rect width="${w}" height="${h}" rx="${r}" fill="url(#chip${sid})"/>
    <g stroke="${t.chipEdge}" stroke-width="1.6" opacity=".55" fill="none">
      <line x1="0" y1="${Y(0.34)}" x2="${w}" y2="${Y(0.34)}"/>
      <line x1="0" y1="${Y(0.66)}" x2="${w}" y2="${Y(0.66)}"/>
      <line x1="${X(0.3)}" y1="0" x2="${X(0.3)}" y2="${h}"/>
      <line x1="${X(0.7)}" y1="0" x2="${X(0.7)}" y2="${h}"/>
    </g>
    <rect x="${X(0.3)}" y="${Y(0.34)}" width="${X(0.4)}" height="${Y(0.32)}" rx="4" fill="${t.chipA}" stroke="${t.chipEdge}" stroke-width="1" opacity=".9"/>
    <ellipse cx="${X(0.24)}" cy="${Y(0.2)}" rx="16" ry="7" fill="#ffffff" opacity=".35"/>
    <rect width="${w}" height="${h}" rx="${r}" fill="none" stroke="${t.chipEdge}" stroke-width="1"/>
  </g>`;
}

function brandMark(cx: number, cy: number, t: Theme, style: CardStyle): string {
  if (style === "openai") {
    let ring = "";
    for (let i = 0; i < 16; i++) {
      const a = (i / 16) * Math.PI * 2;
      ring += `<circle cx="${(cx + 44 * Math.cos(a)).toFixed(1)}" cy="${(cy + 44 * Math.sin(a)).toFixed(1)}" r="2.2" fill="${t.accent}" opacity="${(0.3 + 0.5 * (i / 16)).toFixed(2)}"/>`;
    }
    return `${ring}<circle cx="${cx}" cy="${cy}" r="30" fill="none" stroke="${t.fg}" stroke-width="3"/><path d="M${cx - 12} ${cy - 9} L${cx - 2} ${cy} L${cx - 12} ${cy + 9}" fill="none" stroke="${t.fg}" stroke-width="3.4" stroke-linecap="round" stroke-linejoin="round"/><line x1="${cx + 1}" y1="${cy + 11}" x2="${cx + 14}" y2="${cy + 11}" stroke="${t.fg}" stroke-width="3.4" stroke-linecap="round"/>`;
  }
  let rays = "";
  for (let i = 0; i < 12; i++) {
    const a = (i / 12) * Math.PI * 2;
    rays += `<line x1="${(cx + 16 * Math.cos(a)).toFixed(1)}" y1="${(cy + 16 * Math.sin(a)).toFixed(1)}" x2="${(cx + 42 * Math.cos(a)).toFixed(1)}" y2="${(cy + 42 * Math.sin(a)).toFixed(1)}" stroke="${t.glow}" stroke-width="${i % 2 ? 2.4 : 4}" stroke-linecap="round"/>`;
  }
  return `${rays}<circle cx="${cx}" cy="${cy}" r="13" fill="${t.glow}"/>`;
}

function sparkles(pts: [number, number, number][], color: string): string {
  return pts
    .map(
      ([x, y, r]) =>
        `<path d="M${x} ${y - r} L${x + r * 0.28} ${y - r * 0.28} L${x + r} ${y} L${x + r * 0.28} ${y + r * 0.28} L${x} ${y + r} L${x - r * 0.28} ${y + r * 0.28} L${x - r} ${y} L${x - r * 0.28} ${y - r * 0.28} Z" fill="${color}" opacity=".7"/>`,
    )
    .join("");
}

// qrSVG renders an offline QR matrix as <rect>s. ALWAYS dark modules on a light
// quiet-zone panel (regardless of card tone) so it stays scannable — on a dark
// card the white panel makes the QR pop; matching the module colour to the card
// would make it invisible (the bug this replaces).
function qrSVG(text: string, x: number, y: number, size: number): string {
  let qr: ReturnType<typeof qrcode>;
  try {
    qr = qrcode(0, "M");
    qr.addData(text || "https://hypitoken.com");
    qr.make();
  } catch {
    return "";
  }
  const pad = size * 0.08;
  const inner = size - pad * 2;
  const count = qr.getModuleCount();
  const cell = inner / count;
  let rects = `<rect x="${x}" y="${y}" width="${size}" height="${size}" rx="${(size * 0.08).toFixed(1)}" fill="#ffffff"/>`;
  for (let r = 0; r < count; r++) {
    for (let col = 0; col < count; col++) {
      if (qr.isDark(r, col)) {
        rects += `<rect x="${(x + pad + col * cell).toFixed(2)}" y="${(y + pad + r * cell).toFixed(2)}" width="${cell.toFixed(2)}" height="${cell.toFixed(2)}" fill="#0b0b0d"/>`;
      }
    }
  }
  return rects;
}

function buildFrontSVG(p: TokenCardProps, uid: string): string {
  const t = THEMES[`${p.style}-${p.tone}`];
  const sid = `${uid.replace(/[^a-zA-Z0-9]/g, "")}f`;
  const dark = p.tone === "dark";
  const lines = (p.tagline || "").split("\n").slice(0, 2);
  const markX = 866;
  const markY = 120;
  const headFont = p.style === "claude" ? SERIF : SANS;
  return `<svg viewBox="0 0 ${W} ${H}" xmlns="http://www.w3.org/2000/svg" font-family="${headFont}">
  <defs>
    <clipPath id="card${sid}"><rect width="${W}" height="${H}" rx="${R}"/></clipPath>
    <linearGradient id="bg${sid}" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="${t.bg[0]}"/><stop offset=".55" stop-color="${t.bg[1]}"/><stop offset="1" stop-color="${t.bg[2]}"/>
    </linearGradient>
    <linearGradient id="chip${sid}" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="${t.chipA}"/><stop offset=".5" stop-color="${t.chipB}"/><stop offset="1" stop-color="${t.chipA}"/>
    </linearGradient>
    <linearGradient id="metal${sid}" x1="0.1" y1="0" x2="0" y2="1">
      ${
        dark
          ? `<stop offset="0" stop-color="#ffffff"/><stop offset=".42" stop-color="#dbe2ec"/><stop offset=".52" stop-color="#ffffff"/><stop offset=".64" stop-color="#b4bdca"/><stop offset="1" stop-color="#eaeff5"/>`
          : `<stop offset="0" stop-color="${t.chipA}"/><stop offset=".5" stop-color="${t.foil}"/><stop offset="1" stop-color="${t.chipB}"/>`
      }
    </linearGradient>
    <linearGradient id="foil${sid}" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="${t.foil}" stop-opacity="0"/><stop offset=".5" stop-color="${t.foil}" stop-opacity=".9"/><stop offset="1" stop-color="${t.foil}" stop-opacity="0"/>
    </linearGradient>
    <radialGradient id="vig${sid}" cx="50%" cy="42%" r="80%">
      <stop offset="58%" stop-color="#000000" stop-opacity="0"/><stop offset="100%" stop-color="#000000" stop-opacity="${dark ? 0.5 : 0.06}"/>
    </radialGradient>
    <filter id="soft${sid}"><feGaussianBlur stdDeviation="36"/></filter>
  </defs>
  <g clip-path="url(#card${sid})">
    <rect width="${W}" height="${H}" fill="url(#bg${sid})"/>
    <circle cx="838" cy="150" r="220" fill="${t.glow}" opacity="${dark ? 0.22 : 0.14}" filter="url(#soft${sid})"/>
    <circle cx="120" cy="600" r="200" fill="${t.glow2}" opacity="${dark ? 0.16 : 0.1}" filter="url(#soft${sid})"/>
    <g opacity="${dark ? 0.5 : 0.4}">${guilloche(W - 150, 470, t.guilloche)}</g>
    <rect width="${W}" height="${H}" fill="url(#vig${sid})"/>
    <rect x="22" y="22" width="${W - 44}" height="${H - 44}" rx="${R - 16}" fill="none" stroke="${t.hair}" stroke-width="1.4"/>
    <rect x="22" y="22" width="${W - 44}" height="${H - 44}" rx="${R - 16}" fill="none" stroke="url(#foil${sid})" stroke-width="1" opacity="${dark ? 0.8 : 0.55}"/>
    <g opacity="${dark ? 0.92 : 0.62}">${sparkles(
      [
        [560, 168, 6],
        [636, 130, 5],
        [716, 202, 6.5],
        [598, 250, 4.5],
        [700, 150, 5],
      ],
      t.accent,
    )}</g>

    <text x="64" y="92" font-size="30" font-weight="700" letter-spacing="6" fill="${t.fg}">HYPITOKEN</text>
    <text x="64" y="120" font-size="14" letter-spacing="5" fill="${t.muted}" font-family="${MONO}">TOKEN CARD · 通证卡</text>

    ${brandMark(markX, markY, t, p.style)}

    <g transform="translate(${W - 64},198)">
      <rect x="-150" y="-26" width="150" height="36" rx="18" fill="${dark ? "rgba(255,255,255,.07)" : "rgba(0,0,0,.05)"}" stroke="${t.hair}"/>
      <text x="-75" y="-2" text-anchor="middle" font-size="15" font-weight="700" letter-spacing="3" fill="${t.fg}" font-family="${MONO}">${esc(p.tier || "MEMBER")}</text>
    </g>

    ${chip(70, 236, t, sid)}

    <text x="70" y="392" font-size="54" font-weight="${p.style === "claude" ? 500 : 600}" fill="${t.fg}" letter-spacing="-1" font-style="${p.style === "claude" ? "italic" : "normal"}">${esc(lines[0] || "")}</text>
    ${lines[1] ? `<text x="70" y="450" font-size="54" font-weight="${p.style === "claude" ? 500 : 600}" fill="${t.fg}" letter-spacing="-1" font-style="${p.style === "claude" ? "italic" : "normal"}">${esc(lines[1])}</text>` : ""}
    ${p.message ? `<text x="74" y="${lines[1] ? 488 : 432}" font-size="17" letter-spacing="2" fill="${t.muted}" font-family="${SANS}">${esc(p.message)}</text>` : ""}

    <text x="${W - 64}" y="300" text-anchor="end" font-size="68" font-weight="700" fill="url(#metal${sid})" font-family="${MONO}">${esc(p.value)}</text>
    <text x="${W - 64}" y="330" text-anchor="end" font-size="13" letter-spacing="2" fill="${t.muted}" font-family="${MONO}">${esc(p.caption || "STORED VALUE · 储值额度")}</text>

    ${qrSVG(p.redeemUrl || "", W - 168, 372, 96)}

    <text x="70" y="556" font-size="28" letter-spacing="5" fill="${t.fg}" font-family="${MONO}" opacity=".92">${esc(p.code)}</text>
    <text x="70" y="600" font-size="13" letter-spacing="2" fill="${t.muted}" font-family="${MONO}">BEARER · 持卡即享</text>
    ${p.serial ? `<text x="70" y="618" font-size="11" letter-spacing="2" fill="${t.muted}" font-family="${MONO}" opacity=".75">${esc(p.serial)}</text>` : ""}
    <text x="${W - 64}" y="600" text-anchor="end" font-size="11" letter-spacing="3" fill="${t.muted}" font-family="${MONO}">${esc(p.style.toUpperCase())} · ${esc(p.tone.toUpperCase())} EDITION</text>
  </g>
</svg>`;
}

const DEFAULT_TERMS = [
  "本卡为 HypiToken 赠礼,到账为等额平台钱包额度 (USD)。",
  "平台按官方用量 × 分组倍率实时结算,余额可用于全部模型。",
  "不可兑换现金,不设找零。Non-cash welfare gift · settles at usage × multiplier.",
];

function buildBackSVG(p: TokenCardProps, uid: string): string {
  const t = THEMES[`${p.style}-${p.tone}`];
  const sid = `${uid.replace(/[^a-zA-Z0-9]/g, "")}b`;
  const dark = p.tone === "dark";
  const stripe = dark ? "#07070a" : "#1a1a1f";
  const panel = dark ? "#ece9e2" : "#fbfaf6";
  const headFont = p.style === "claude" ? SERIF : SANS;
  return `<svg viewBox="0 0 ${W} ${H}" xmlns="http://www.w3.org/2000/svg" font-family="${MONO}">
  <defs>
    <clipPath id="card${sid}"><rect width="${W}" height="${H}" rx="${R}"/></clipPath>
    <linearGradient id="bg${sid}" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="${t.bg[1]}"/><stop offset="1" stop-color="${t.bg[2]}"/>
    </linearGradient>
    <linearGradient id="foilb${sid}" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="${t.foil}" stop-opacity="0"/><stop offset=".5" stop-color="${t.foil}" stop-opacity=".9"/><stop offset="1" stop-color="${t.foil}" stop-opacity="0"/>
    </linearGradient>
  </defs>
  <g clip-path="url(#card${sid})">
    <rect width="${W}" height="${H}" fill="url(#bg${sid})"/>
    <g opacity="${dark ? 0.5 : 0.4}">${guilloche(500, 520, t.guilloche)}</g>
    <rect x="0" y="78" width="${W}" height="104" fill="${stripe}"/>
    <rect x="0" y="78" width="${W}" height="104" fill="url(#foilb${sid})" opacity=".25"/>

    <g transform="translate(64,212)">
      <rect width="560" height="92" rx="10" fill="${panel}"/>
      <rect width="560" height="92" rx="10" fill="none" stroke="${t.hair}"/>
      ${Array.from({ length: 14 })
        .map(
          (_, i) =>
            `<rect x="${10 + i * 40}" y="10" width="22" height="72" fill="${t.accent}" opacity=".08"/>`,
        )
        .join("")}
      <text x="18" y="34" font-size="12" letter-spacing="2" fill="#6b6760">REDEEM CODE · 兑换码</text>
      <text x="18" y="70" font-size="26" letter-spacing="2" fill="#141413" font-weight="700">${esc(p.code)}</text>
    </g>

    <g transform="translate(${W - 64 - 164},198)">
      <rect width="164" height="164" rx="14" fill="#fbfaf6" stroke="${t.hair}"/>
      ${qrSVG(p.redeemUrl || "", 20, 20, 124)}
    </g>
    <text x="${W - 64 - 82}" y="384" text-anchor="middle" font-size="11" letter-spacing="1.5" fill="${t.muted}">扫码兑换 · SCAN TO REDEEM</text>

    <text x="64" y="350" font-size="13" letter-spacing="1" fill="${t.fg}" font-weight="700">使用条款 · TERMS</text>
    ${DEFAULT_TERMS.map((l, i) => `<text x="64" y="${380 + i * 25}" font-size="13.5" fill="${t.muted}" font-family="${SANS}">${esc(l)}</text>`).join("")}

    <text x="64" y="566" font-size="20" font-weight="700" letter-spacing="4" fill="${t.fg}" font-family="${headFont}">HYPITOKEN</text>
    <text x="64" y="592" font-size="13" letter-spacing="2" fill="${t.muted}">one card · every model</text>
    <text x="${W - 64}" y="592" text-anchor="end" font-size="12" letter-spacing="3" fill="${t.muted}">${esc(p.style.toUpperCase())} · ${esc(p.tone.toUpperCase())}</text>
  </g>
</svg>`;
}

// ── R3F 3D scene (ported from the design's WebGL layer) ─────────────────────

// Iridescent morphing crystal: normal-wave breathing displacement + Fresnel rim
// + view-angle iridescence (oil-slick / holo foil). Ported from the design.
const CRYSTAL_VERT = `
  uniform float u_time; uniform float u_amp;
  varying vec3 vN; varying vec3 vView; varying float vW;
  void main(){
    float w = sin(normal.x*3.0 + u_time)*0.5 + sin(normal.y*4.0 - u_time*0.8)*0.3 + sin(normal.z*5.0 + u_time*1.2)*0.2;
    vW = w;
    vec3 p = position + normal * w * u_amp;
    vN = normalize(normalMatrix * normal);
    vec4 mv = modelViewMatrix * vec4(p,1.0);
    vView = -mv.xyz;
    gl_Position = projectionMatrix * mv;
  }`;
const CRYSTAL_FRAG = `
  precision highp float;
  uniform vec3 u_c1; uniform vec3 u_c2; uniform float u_time; uniform float u_light;
  varying vec3 vN; varying vec3 vView; varying float vW;
  void main(){
    vec3 N = normalize(vN); vec3 V = normalize(vView);
    float fres = pow(1.0 - clamp(dot(N,V),0.0,1.0), 2.2);
    float hue = vW*0.6 + dot(N,V)*0.5 + u_time*0.06;
    float m = 0.5 + 0.5*sin(hue*6.2831);
    vec3 base = mix(u_c1, u_c2, m);
    vec3 shimmer = vec3(0.5+0.5*sin(hue*6.2831), 0.5+0.5*sin(hue*6.2831+2.094), 0.5+0.5*sin(hue*6.2831+4.188));
    vec3 col = base*(0.25+0.55*fres) + shimmer*fres*0.55;
    if(u_light>0.5){
      vec3 o = vec3(0.5) + (col-0.32)*0.64;
      gl_FragColor = vec4(clamp(o,0.0,1.0), 0.5 + fres*0.45);
    } else {
      gl_FragColor = vec4(col, 0.62 + fres*0.38);
    }
  }`;
// Curl-drift light particles (procedural in the vertex shader).
const PARTICLE_VERT = `
  uniform float u_time; uniform float u_light; uniform float u_pr;
  uniform vec3 u_c1; uniform vec3 u_c2;
  attribute float aSeed;
  varying float vA; varying vec3 vCol;
  void main(){
    vec3 p = position;
    float s = aSeed;
    float t = u_time*0.16 + s*6.2831;
    p.x += sin(t + p.y*1.6)*0.55;
    p.y += cos(t*0.9 + p.z*1.3)*0.5;
    p.z += sin(t*1.1 + p.x*1.5)*0.55;
    vec4 mv = modelViewMatrix * vec4(p,1.0);
    float tw = 0.45 + 0.55*sin(t*2.3 + s*3.0);
    gl_PointSize = (u_light>0.5 ? 16.0 : 26.0) * u_pr * tw / max(0.1,-mv.z);
    gl_Position = projectionMatrix * mv;
    vA = tw;
    vCol = mix(u_c1, u_c2, fract(s*7.31));
  }`;
const PARTICLE_FRAG = `
  precision highp float;
  varying float vA; varying vec3 vCol; uniform float u_light;
  void main(){
    vec2 uv = gl_PointCoord - 0.5;
    float d = length(uv);
    if(d>0.5) discard;
    float a = smoothstep(0.5,0.0,d);
    gl_FragColor = vec4(vCol, a*vA*(u_light>0.5?0.55:0.95));
  }`;

const prefersReducedMotion = () =>
  typeof window !== "undefined" &&
  !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

// softDotTexture is the soft round sprite for the depth stars.
function softDotTexture(): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = 64;
  c.height = 64;
  const x = c.getContext("2d");
  if (x) {
    const g = x.createRadialGradient(32, 32, 0, 32, 32, 32);
    g.addColorStop(0, "rgba(255,255,255,1)");
    g.addColorStop(0.45, "rgba(255,255,255,0.5)");
    g.addColorStop(1, "rgba(255,255,255,0)");
    x.fillStyle = g;
    x.fillRect(0, 0, 64, 64);
  }
  return new THREE.CanvasTexture(c);
}

function Stars({ light, color }: { light: boolean; color: THREE.Color }) {
  const geo = useMemo(() => {
    const n = light ? 30 : 54;
    const sp = new Float32Array(n * 3);
    for (let i = 0; i < n; i++) {
      sp[i * 3] = (Math.random() * 2 - 1) * 3.2;
      sp[i * 3 + 1] = (Math.random() * 2 - 1) * 2.0;
      sp[i * 3 + 2] = (Math.random() * 2 - 1) * 2.2;
    }
    const g = new THREE.BufferGeometry();
    g.setAttribute("position", new THREE.BufferAttribute(sp, 3));
    return g;
  }, [light]);
  const tex = useMemo(softDotTexture, []);
  return (
    <points geometry={geo}>
      <pointsMaterial
        map={tex}
        color={color}
        size={light ? 0.07 : 0.11}
        sizeAttenuation
        transparent
        depthWrite={false}
        blending={light ? THREE.NormalBlending : THREE.AdditiveBlending}
        opacity={light ? 0.7 : 0.9}
      />
    </points>
  );
}

function Crystal({
  style,
  light,
  c1,
  c2,
  animate,
}: {
  style: CardStyle;
  light: boolean;
  c1: THREE.Color;
  c2: THREE.Color;
  animate: boolean;
}) {
  const ref = useRef<THREE.Mesh>(null);
  const uniforms = useMemo(
    () => ({
      u_time: { value: 0 },
      u_amp: { value: light ? 0.12 : 0.18 },
      u_light: { value: light ? 1 : 0 },
      u_c1: { value: c1 },
      u_c2: { value: c2 },
    }),
    [light, c1, c2],
  );
  useFrame((_, dt) => {
    if (!animate || !ref.current) return;
    ref.current.rotation.y += dt * 0.32;
    ref.current.rotation.x += dt * 0.2;
    uniforms.u_time.value += dt;
  });
  return (
    <mesh ref={ref}>
      {style === "claude" ? (
        <octahedronGeometry args={[0.82, 0]} />
      ) : (
        <icosahedronGeometry args={[0.8, 1]} />
      )}
      <shaderMaterial
        vertexShader={CRYSTAL_VERT}
        fragmentShader={CRYSTAL_FRAG}
        uniforms={uniforms}
        transparent
        depthWrite={false}
        side={THREE.DoubleSide}
        blending={light ? THREE.NormalBlending : THREE.AdditiveBlending}
      />
    </mesh>
  );
}

function Shell({
  style,
  light,
  color,
  animate,
}: {
  style: CardStyle;
  light: boolean;
  color: THREE.Color;
  animate: boolean;
}) {
  const ref = useRef<THREE.LineSegments>(null);
  const geo = useMemo(
    () =>
      new THREE.WireframeGeometry(
        style === "claude"
          ? new THREE.OctahedronGeometry(1.04, 0)
          : new THREE.IcosahedronGeometry(1.02, 1),
      ),
    [style],
  );
  useFrame((_, dt) => {
    if (!animate || !ref.current) return;
    ref.current.rotation.y -= dt * 0.2;
    ref.current.rotation.x += dt * 0.12;
  });
  return (
    <lineSegments ref={ref} geometry={geo}>
      <lineBasicMaterial
        color={color}
        transparent
        opacity={light ? 0.22 : 0.55}
        depthWrite={false}
      />
    </lineSegments>
  );
}

function Particles({
  light,
  c1,
  c2,
  animate,
}: {
  light: boolean;
  c1: THREE.Color;
  c2: THREE.Color;
  animate: boolean;
}) {
  const geo = useMemo(() => {
    const n = light ? 60 : 120;
    const pp = new Float32Array(n * 3);
    const ps = new Float32Array(n);
    for (let i = 0; i < n; i++) {
      const r = 1.1 + Math.random() * 1.7;
      const th = Math.random() * 6.2831;
      const ph = Math.acos(Math.random() * 2 - 1);
      pp[i * 3] = Math.sin(ph) * Math.cos(th) * r;
      pp[i * 3 + 1] = Math.cos(ph) * r * 0.82;
      pp[i * 3 + 2] = Math.sin(ph) * Math.sin(th) * r;
      ps[i] = Math.random();
    }
    const g = new THREE.BufferGeometry();
    g.setAttribute("position", new THREE.BufferAttribute(pp, 3));
    g.setAttribute("aSeed", new THREE.BufferAttribute(ps, 1));
    return g;
  }, [light]);
  const dpr = typeof window !== "undefined" ? Math.min(2, window.devicePixelRatio || 1) : 1;
  const uniforms = useMemo(
    () => ({
      u_time: { value: 0 },
      u_light: { value: light ? 1 : 0 },
      u_pr: { value: dpr },
      u_c1: { value: c1 },
      u_c2: { value: c2 },
    }),
    [light, dpr, c1, c2],
  );
  useFrame((_, dt) => {
    if (animate) uniforms.u_time.value += dt;
  });
  return (
    <points geometry={geo}>
      <shaderMaterial
        vertexShader={PARTICLE_VERT}
        fragmentShader={PARTICLE_FRAG}
        uniforms={uniforms}
        transparent
        depthWrite={false}
        blending={light ? THREE.NormalBlending : THREE.AdditiveBlending}
      />
    </points>
  );
}

function CardScene({
  style,
  tone,
  animate,
}: {
  style: CardStyle;
  tone: CardTone;
  animate: boolean;
}) {
  const t = THEMES[`${style}-${tone}`];
  const light = tone === "light";
  const c1 = useMemo(() => new THREE.Color(t.glow), [t.glow]);
  const cAccent = useMemo(() => new THREE.Color(t.accent), [t.accent]);
  const c2 = useMemo(() => new THREE.Color(t.accent2), [t.accent2]);
  const group = useRef<THREE.Group>(null);
  useFrame((state) => {
    if (!animate || !group.current) return;
    const tt = state.clock.elapsedTime;
    group.current.rotation.y = tt * 0.05;
    group.current.rotation.x = Math.sin(tt * 0.06) * 0.12;
  });
  return (
    <group ref={group}>
      <Stars light={light} color={light ? c2 : c1} />
      <Crystal style={style} light={light} c1={c1} c2={cAccent} animate={animate} />
      <Shell style={style} light={light} color={cAccent} animate={animate} />
      <Particles light={light} c1={c1} c2={c2} animate={animate} />
    </group>
  );
}

// CardAura overlays the design's animated 3D scene on the static SVG face,
// brand-blended (screen on dark, overlay on light).
function CardAura({ style, tone }: { style: CardStyle; tone: CardTone }) {
  const light = tone === "light";
  const animate = !prefersReducedMotion();
  return (
    <div
      className="pointer-events-none absolute inset-0"
      style={{ mixBlendMode: light ? "overlay" : "screen", opacity: light ? 0.5 : 0.62 }}
    >
      <Canvas
        gl={{
          alpha: true,
          antialias: true,
          premultipliedAlpha: false,
          powerPreference: "low-power",
        }}
        dpr={[1, 2]}
        camera={{ fov: 50, position: [0, 0, 4.2], near: 0.1, far: 100 }}
        frameloop={animate ? "always" : "demand"}
        style={{ width: "100%", height: "100%" }}
      >
        <CardScene style={style} tone={tone} animate={animate} />
      </Canvas>
    </div>
  );
}

// ── Public API ──────────────────────────────────────────────────────────────

/** Serialise the front face to a standalone SVG string (for download). */
export function tokenCardSVG(props: TokenCardProps): string {
  return buildFrontSVG(props, "x");
}

/** Rasterise the front face to a PNG blob at the given pixel width. */
export async function tokenCardPNG(props: TokenCardProps, pxWidth = 1012): Promise<Blob> {
  const svg = buildFrontSVG(props, "x");
  const url = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  const img = new Image();
  img.crossOrigin = "anonymous";
  await new Promise<void>((resolve, reject) => {
    img.onload = () => resolve();
    img.onerror = () => reject(new Error("svg load failed"));
    img.src = url;
  });
  const scale = pxWidth / W;
  const canvas = document.createElement("canvas");
  canvas.width = pxWidth;
  canvas.height = Math.round(H * scale);
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("no 2d context");
  ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
  return await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob failed"))), "image/png");
  });
}

function Face({ svg, aura }: { svg: string; aura?: React.ReactNode }) {
  return (
    <div
      className="relative w-full overflow-hidden rounded-[3.3%] shadow-2xl ring-1 ring-white/10"
      style={{ aspectRatio: `${W} / ${H}` }}
    >
      {/* biome-ignore lint/security/noDangerouslySetInnerHtml: locally-built, escaped SVG (no user HTML) */}
      <div className="absolute inset-0" dangerouslySetInnerHTML={{ __html: svg }} />
      {aura}
    </div>
  );
}

/** The in-app card renderer: front face (with the live 3D scene) stacked
 *  above the back face, both visible at once. */
export function TokenCard(props: TokenCardProps) {
  const rawId = useId();
  const front = useMemo(() => buildFrontSVG(props, rawId), [props, rawId]);
  const back = useMemo(() => buildBackSVG(props, rawId), [props, rawId]);
  return (
    <div className="flex flex-col gap-4">
      <Face svg={front} aura={<CardAura style={props.style} tone={props.tone} />} />
      <Face svg={back} />
    </div>
  );
}
