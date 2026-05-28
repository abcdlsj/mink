function hash32(s) {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h >>> 0;
}

function identiconSVG(seed, kind) {
  const h = hash32(seed || "anon");
  const grid = 5;
  const palette = {
    agent:   ["#3b6fa8", "#4a7fb8"],
    user:    ["#5b6470", "#6f7884"],
    channel: ["#4f8c54", "#5e9b63"],
    thread:  ["#9c5b97", "#aa6ea6"],
  };
  const [c1] = palette[kind] || palette.agent;
  const cells = [];
  for (let y = 0; y < grid; y++) {
    for (let x = 0; x < Math.ceil(grid / 2); x++) {
      const bit = (h >>> (y * 3 + x)) & 1;
      const fill = bit ? c1 : null;
      if (fill) {
        cells.push(`<rect x="${x}" y="${y}" width="1" height="1" fill="${fill}"/>`);
        if (x !== grid - 1 - x) {
          cells.push(`<rect x="${grid - 1 - x}" y="${y}" width="1" height="1" fill="${fill}"/>`);
        }
      }
    }
  }
  return `<svg class="identicon" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${grid} ${grid}" shape-rendering="crispEdges">${cells.join("")}</svg>`;
}
