package platform

const webMissingPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Mink Web</title>
  <style>
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      background: #f6f1e8;
      color: #1e1b18;
      font-family: "IBM Plex Sans", "Noto Sans SC", sans-serif;
    }
    main {
      width: min(560px, calc(100vw - 32px));
      border: 2px solid #1e1b18;
      background: #fbf8f2;
      padding: 24px;
    }
    h1 {
      margin: 0 0 12px;
      font-size: 28px;
      text-transform: uppercase;
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
    }
    p {
      margin: 0 0 8px;
      line-height: 1.5;
    }
    code {
      font-family: "IBM Plex Mono", monospace;
    }
  </style>
</head>
<body>
  <main>
    <h1>Web Assets Missing</h1>
    <p>Frontend assets were not found.</p>
    <p>Run <code>cd web && pnpm build</code>, then start <code>mink web</code> again.</p>
  </main>
</body>
</html>`
