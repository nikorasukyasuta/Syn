package web

// BaseCSS provides responsive, modern styling for the web portal.
const BaseCSS = `
:root {
  --bg-primary: #0b0f19;
  --bg-secondary: #131b2e;
  --bg-card: #182238;
  --border-color: #23304c;
  --text-primary: #f1f5f9;
  --text-secondary: #94a3b8;
  --accent: #5865f2;
  --accent-hover: #4752c4;
  --danger: #ef4444;
  --warning: #f59e0b;
  --success: #10b981;
}

* {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  background-color: var(--bg-primary);
  color: var(--text-primary);
  line-height: 1.6;
  padding: 2rem 1rem;
}

.container {
  max-width: 860px;
  margin: 0 auto;
}

header {
  border-bottom: 1px solid var(--border-color);
  padding-bottom: 1.5rem;
  margin-bottom: 2rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 1rem;
}

.logo-area {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.logo-badge {
  background: linear-gradient(135deg, #5865f2, #8b5cf6);
  color: white;
  font-weight: 700;
  font-size: 1.25rem;
  padding: 0.5rem 0.85rem;
  border-radius: 12px;
  box-shadow: 0 4px 14px rgba(88, 101, 242, 0.4);
}

.logo-title {
  font-size: 1.5rem;
  font-weight: 700;
}

nav a {
  color: var(--text-secondary);
  text-decoration: none;
  font-weight: 500;
  margin-left: 1.25rem;
  transition: color 0.2s;
}

nav a:hover, nav a.active {
  color: var(--accent);
}

.content-card {
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-radius: 16px;
  padding: 2.5rem;
  box-shadow: 0 10px 25px rgba(0, 0, 0, 0.3);
}

h1 {
  font-size: 2rem;
  margin-bottom: 0.5rem;
  color: #fff;
}

.meta-info {
  color: var(--text-secondary);
  font-size: 0.9rem;
  margin-bottom: 2rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid var(--border-color);
}

h2 {
  font-size: 1.35rem;
  margin: 2rem 0 0.75rem 0;
  color: #e2e8f0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

h2:first-of-type {
  margin-top: 1rem;
}

p {
  margin-bottom: 1rem;
  color: #cbd5e1;
}

ul {
  margin: 0.5rem 0 1.25rem 1.5rem;
  color: #cbd5e1;
}

li {
  margin-bottom: 0.5rem;
}

.callout {
  background: var(--bg-card);
  border-left: 4px solid var(--accent);
  padding: 1rem 1.25rem;
  border-radius: 0 8px 8px 0;
  margin: 1.5rem 0;
}

.callout-warning {
  border-left-color: var(--warning);
}

.callout-danger {
  border-left-color: var(--danger);
}

.badge {
  display: inline-block;
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  font-weight: 600;
  border-radius: 6px;
  background: var(--border-color);
  color: var(--text-secondary);
}

footer {
  text-align: center;
  margin-top: 3rem;
  padding-top: 1.5rem;
  border-top: 1px solid var(--border-color);
  color: var(--text-secondary);
  font-size: 0.85rem;
}

@media (max-width: 640px) {
  body { padding: 1rem 0.5rem; }
  .content-card { padding: 1.5rem 1rem; }
  h1 { font-size: 1.6rem; }
  header { flex-direction: column; align-items: flex-start; }
  nav a { margin-left: 0; margin-right: 1rem; }
}
`

// TermsHTML provides the rendered Terms and Conditions webpage.
const TermsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Terms & Conditions • Syn Discord Suite</title>
  <style>` + BaseCSS + `</style>
</head>
<body>
  <div class="container">
    <header>
      <div class="logo-area">
        <div class="logo-badge">SYN</div>
        <div class="logo-title">Syn Discord Suite</div>
      </div>
      <nav>
        <a href="/">Home</a>
        <a href="/terms" class="active">Terms & Conditions</a>
        <a href="/privacy">Privacy Policy</a>
      </nav>
    </header>

    <main class="content-card">
      <h1>Terms & Conditions</h1>
      <div class="meta-info">
        <span>Effective Date: October 22, 2026</span> • <span>Version 1.2.2</span>
      </div>

      <div class="callout callout-warning">
        <strong>Important Notice:</strong> By participating in this Discord server or interacting with the Syn Bot, you acknowledge and agree to comply with all server rules, safety protocols, and the terms set forth below.
      </div>

      <h2>1. Acceptance of Terms</h2>
      <p>By accessing or interacting with the Syn Discord Bot and the associated Discord server, you agree to be bound by these Terms and Conditions and all applicable laws and regulations. If you do not agree with any of these terms, you are prohibited from using the bot or participating in the community.</p>

      <h2>2. Age Requirement & 18+ Verification</h2>
      <p>This community strictly enforces an adult (18+) access policy for adult, kink, and verified content channels:</p>
      <ul>
        <li>You must be at least 18 years of age to complete verification and access age-restricted channels.</li>
        <li>Submitting forged, fraudulent, or altered age verification credentials will result in an immediate and permanent server ban without appeal.</li>
        <li>Staff members verify credentials in isolated, private ticket channels. Verification records are stored securely in compliance with applicable privacy laws.</li>
      </ul>

      <h2>3. Community Conduct, Boundaries & Safewords</h2>
      <p>Mutual respect, safety, and clear boundaries are foundational requirements in this server:</p>
      <ul>
        <li><strong>Respect for RED / Safewords:</strong> When a user or staff member issues a <code>/red</code> command or reacts with 🛑, all participants in the conversation must immediately halt the topic and change subjects. Continuing an argument or invasive behavior after RED may result in immediate timeouts or removal.</li>
        <li><strong>Automated Word Filters:</strong> Messages containing prohibited hate speech, severe slurs, or banned terms are automatically and immediately deleted. Each violation triggers an automated staff review ticket where staff may issue a formal moderation warning or take further disciplinary action.</li>
      </ul>

      <h2>4. Temporary Media & "Flash and Dash" Channels</h2>
      <p>The server provides designated temporary photo channels ("Flash and Dash") for sharing ephemeral media:</p>
      <ul>
        <li><strong>Mandatory Photo Spoilering:</strong> All photos and media uploaded in Flash and Dash channels must be spoilered. The Syn Bot automatically intercepts and re-posts unspoilered photos with spoiler tags to protect member browsing preferences.</li>
        <li><strong>Automated Permanent Deletion:</strong> All photos and media in Flash and Dash channels are subject to an automated time-to-live (TTL) expiration timer configured by staff. When the timer expires, messages and media are permanently and irreversibly deleted from Discord.</li>
        <li>Users must not screenshot, re-distribute, or save media shared in Flash and Dash channels without the express consent of the original poster.</li>
      </ul>

      <h2>5. Support & Moderation Tickets</h2>
      <p>The bot manages private support, report, and verification channels:</p>
      <ul>
        <li>Ticket channels are private to the creator, server administrators, and authorized moderators.</li>
        <li>Staff resolution decisions and ticket summaries are logged for accountability and server security.</li>
      </ul>

      <h2>6. Termination and Moderation</h2>
      <p>Server administrators reserve the right to revoke server access, remove roles, or issue warnings at their sole discretion for conduct that disrupts community safety or violates Discord's Terms of Service.</p>
    </main>

    <footer>
      &copy; 2026 Syn Discord Suite • Open Source • Hosted on Secure EC2 Infrastructure
    </footer>
  </div>
</body>
</html>
`

// PrivacyHTML provides the rendered Privacy Policy webpage.
const PrivacyHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Privacy Policy • Syn Discord Suite</title>
  <style>` + BaseCSS + `</style>
</head>
<body>
  <div class="container">
    <header>
      <div class="logo-area">
        <div class="logo-badge">SYN</div>
        <div class="logo-title">Syn Discord Suite</div>
      </div>
      <nav>
        <a href="/">Home</a>
        <a href="/terms">Terms & Conditions</a>
        <a href="/privacy" class="active">Privacy Policy</a>
      </nav>
    </header>

    <main class="content-card">
      <h1>Privacy Policy</h1>
      <div class="meta-info">
        <span>Effective Date: October 22, 2026</span> • <span>Version 1.2.2</span>
      </div>

      <p>Your privacy and data safety are of the utmost importance to us. This Privacy Policy outlines what information the Syn Discord Bot collects, how it is used, and how temporary data is purged.</p>

      <h2>1. Information We Collect</h2>
      <p>The Syn Bot operates with strict data minimization principles. We only collect information necessary to fulfill moderation, age gating, and community management functions:</p>
      <ul>
        <li><strong>Discord Snowflake Identifiers:</strong> Discord User IDs, Guild IDs, Channel IDs, and Role IDs required to manage role assignments, permissions, and moderation tickets.</li>
        <li><strong>Moderation Warning Logs:</strong> When a filtered word is triggered and staff issues a warning, the bot records the user ID, issuing admin ID, timestamp, and the specific violating snippet to track repeated infractions.</li>
        <li><strong>Ticket Metadata:</strong> Channel IDs, creator IDs, open/close timestamps, and resolution notes for support and age verification tickets.</li>
      </ul>

      <h2>2. What We Do NOT Collect</h2>
      <ul>
        <li>We do not collect real names, email addresses, phone numbers, or offline contact details.</li>
        <li>We do not collect, process, or store payment, banking, or credit card information.</li>
        <li>We do not monitor, store, or log general chat message content outside of explicit moderation incidents and safeword alerts.</li>
      </ul>

      <h2>3. Ephemeral Media & Automatic Deletion ("Flash and Dash")</h2>
      <p>We provide automated privacy safeguards for temporary media shared in "Flash and Dash" channels:</p>
      <ul>
        <li><strong>No Server Image Retention:</strong> Images uploaded by users are stored on Discord's CDN, not on the Syn Bot's database or host filesystem. When the bot auto-spoilers an image, the file stream is processed transiently in-memory and immediately discarded.</li>
        <li><strong>Automatic Deletion Timers:</strong> Messages and photos sent in designated Flash and Dash channels are assigned an expiration timer (configured by server staff). Upon expiration, the bot executes an automated API call to permanently delete the message from Discord.</li>
      </ul>

      <h2>4. Data Storage & Security</h2>
      <p>All persistent database records (such as role mappings, verified age records, and moderation warnings) are stored in an encrypted PostgreSQL database hosted on an isolated private Docker network with no direct public internet exposure. Access is restricted strictly to authorized server administrators through role-based access control (RBAC).</p>

      <h2>5. Your Rights & Data Requests</h2>
      <p>You have the right to request information on records associated with your Discord User ID, or request data erasure upon leaving the server, subject to legitimate moderation recordkeeping. To make a data request, please open a Support Ticket directly in the server using <code>/ticket open type:support</code>.</p>
    </main>

    <footer>
      &copy; 2026 Syn Discord Suite • Open Source • Hosted on Secure EC2 Infrastructure
    </footer>
  </div>
</body>
</html>
`

// HomeHTML provides the index landing page.
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
