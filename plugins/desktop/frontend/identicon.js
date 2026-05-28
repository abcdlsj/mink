function hash32(s) {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h >>> 0;
}

const AGENT_HUES = [
  "#7C8FA8", "#8FA88A", "#B59A85", "#A088A6",
  "#6FA0A0", "#A78D6F", "#9090B5", "#7CAA94",
];
const USER_HUE = "#8A93A0";
const CHANNEL_HUE = "#7CA98A";
const THREAD_HUE = "#A08AAB";

function identiconSVG(seed, kind) {
  const h = hash32(seed || "anon");
  const grid = 5;
  let fill;
  if (kind === "user") fill = USER_HUE;
  else if (kind === "channel") fill = CHANNEL_HUE;
  else if (kind === "thread") fill = THREAD_HUE;
  else fill = AGENT_HUES[h % AGENT_HUES.length];

  const cells = [];
  for (let y = 0; y < grid; y++) {
    for (let x = 0; x < Math.ceil(grid / 2); x++) {
      const bit = (h >>> (y * 3 + x)) & 1;
      if (!bit) continue;
      cells.push(`<rect x="${x}" y="${y}" width="1" height="1" fill="${fill}"/>`);
      if (x !== grid - 1 - x) {
        cells.push(`<rect x="${grid - 1 - x}" y="${y}" width="1" height="1" fill="${fill}"/>`);
      }
    }
  }
  return `<svg class="identicon" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${grid} ${grid}" shape-rendering="crispEdges">${cells.join("")}</svg>`;
}
