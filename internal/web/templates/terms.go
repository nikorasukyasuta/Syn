package templates

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

