// Package templates holds the modular HTML and CSS templates for the Syn public web portal.
package templates

// BaseCSS provides responsive, modern dark-theme styling for the web portal.
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

