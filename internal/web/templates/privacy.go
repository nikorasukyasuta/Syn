package templates

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

