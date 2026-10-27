package templates

// HomeHTML provides the index landing page for the Syn web portal.
const HomeHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Syn Discord Suite • Documentation & Policies</title>
  <style>` + BaseCSS + `
  .hero { text-align: center; margin: 1rem 0 2.5rem 0; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 1.5rem; }
  .card-action { background: var(--bg-card); border: 1px solid var(--border-color); border-radius: 12px; padding: 1.75rem; text-decoration: none; color: inherit; transition: transform 0.2s, border-color 0.2s; }
  .card-action:hover { transform: translateY(-3px); border-color: var(--accent); }
  .card-icon { font-size: 2rem; margin-bottom: 0.75rem; }
  </style>
</head>
<body>
  <div class="container">
    <header>
      <div class="logo-area">
        <div class="logo-badge">SYN</div>
        <div class="logo-title">Syn Discord Suite</div>
      </div>
      <nav>
        <a href="/" class="active">Home</a>
        <a href="/terms">Terms & Conditions</a>
        <a href="/privacy">Privacy Policy</a>
      </nav>
    </header>

    <main class="content-card">
      <div class="hero">
        <h1>Syn Discord Security & Community Suite</h1>
        <p>Enterprise moderation, 18+ age verification, ephemeral photo channels, and ticket management.</p>
      </div>

      <div class="grid">
        <a href="/terms" class="card-action">
          <div class="card-icon">📜</div>
          <h3>Terms & Conditions</h3>
          <p>Read our community rules, 18+ verification requirements, safeword protocols, and Flash & Dash policies.</p>
        </a>

        <a href="/privacy" class="card-action">
          <div class="card-icon">🔒</div>
          <h3>Privacy Policy</h3>
          <p>Learn how we handle user IDs, moderation records, and our automated message deletion safeguards.</p>
        </a>
      </div>
    </main>

    <footer>
      &copy; 2026 Syn Discord Suite • Open Source • Version 1.2.2
    </footer>
  </div>
</body>
</html>
`

