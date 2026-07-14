<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'

const installCommand = 'brew install abdul-hamid-achik/tap/bp'
const copied = ref(false)
const copyMessage = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | undefined

const blueprintSource = `@ "Create a new todo"
POST /api/todos {
  <- title string required
  |> todo = save todo { title: title }
  -> 201 { id: todo.id, title: todo.title }
}`

async function copyInstallCommand() {
  let didCopy = false

  try {
    await navigator.clipboard.writeText(installCommand)
    didCopy = true
  } catch {
    const textarea = document.createElement('textarea')
    textarea.value = installCommand
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    try {
      didCopy = document.execCommand('copy')
    } catch {
      didCopy = false
    } finally {
      textarea.remove()
    }
  }

  copied.value = didCopy
  copyMessage.value = didCopy
    ? 'Homebrew install command copied to clipboard.'
    : 'Unable to copy the install command. Select the command and copy it manually.'
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copied.value = false
    copyMessage.value = ''
  }, 2200)
}

onBeforeUnmount(() => {
  if (copiedTimer) clearTimeout(copiedTimer)
})
</script>

<template>
  <main class="bp-home">
    <section class="bp-hero" aria-labelledby="bp-hero-title">
      <div class="bp-hero-grid" aria-hidden="true"></div>
      <div class="bp-shell bp-hero-layout">
        <div class="bp-hero-copy">
          <p class="bp-eyebrow">
            <span class="bp-eyebrow-mark" aria-hidden="true"></span>
            A Go-built compiler for web services
          </p>
          <h1 id="bp-hero-title">
            Describe the service.
            <span>Generate the code.</span>
          </h1>
          <p class="bp-hero-lede">
            Blueprint turns an intent-first <code>.bp</code> file into a typed,
            runnable Hono or FastAPI project you can inspect, extend, and own.
          </p>

          <div class="bp-hero-actions" role="group" aria-label="Get started with Blueprint">
            <a class="bp-button bp-button-primary" href="/getting-started">
              Build your first service
              <svg viewBox="0 0 20 20" aria-hidden="true">
                <path d="M4 10h11M11 6l4 4-4 4" />
              </svg>
            </a>
            <a class="bp-button bp-button-secondary" href="/language-reference">
              Explore the language
            </a>
          </div>

          <div class="bp-install-block">
            <div class="bp-install-heading">
              <span id="bp-install-title">Install with Homebrew</span>
              <span aria-hidden="true">macOS + Linux</span>
            </div>
            <div class="bp-install" role="group" aria-labelledby="bp-install-title">
              <span class="bp-terminal-mark" aria-hidden="true">$</span>
              <code tabindex="0">{{ installCommand }}</code>
              <button
                class="bp-copy-button"
                :class="{ 'is-copied': copied }"
                type="button"
                :title="copied ? 'Copied' : 'Copy install command'"
                :aria-label="copied ? 'Install command copied' : copyMessage ? 'Copy failed. Try again' : 'Copy install command'"
                @click="copyInstallCommand"
              >
                <svg v-if="!copied" viewBox="0 0 20 20" aria-hidden="true">
                  <rect x="7" y="7" width="9" height="9" rx="2" />
                  <path d="M13 7V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h2" />
                </svg>
                <svg v-else viewBox="0 0 20 20" aria-hidden="true">
                  <path d="m4 10 4 4 8-9" />
                </svg>
                <span class="bp-copy-label">{{ copied ? 'Copied' : 'Copy' }}</span>
              </button>
              <span class="bp-sr-only" aria-live="polite" aria-atomic="true">
                {{ copyMessage }}
              </span>
            </div>
          </div>

          <ul class="bp-hero-proof" aria-label="Blueprint project qualities">
            <li>MIT licensed</li>
            <li>Inspectable output</li>
            <li>No proprietary runtime</li>
          </ul>
        </div>

        <div class="bp-hero-demo" role="group" aria-label="Blueprint source becomes a runnable service">
          <div class="bp-drawing-label" aria-hidden="true">SERVICE / 001</div>
          <figure class="bp-code-window">
            <figcaption>
              <span class="bp-window-dots" aria-hidden="true"><i></i><i></i><i></i></span>
              <span>todo-api.bp</span>
              <span class="bp-file-state">checked</span>
            </figcaption>
            <pre tabindex="0"><code>{{ blueprintSource }}</code></pre>
          </figure>

          <div class="bp-compile-rail" aria-hidden="true">
            <span>bp build</span>
            <i></i>
            <svg viewBox="0 0 24 12"><path d="M1 6h20M16 1l5 5-5 5" /></svg>
          </div>

          <div class="bp-output-card">
            <div class="bp-output-heading">
              <span>generated/</span>
              <span>ready to run</span>
            </div>
            <ul aria-label="Generated project contents">
              <li><span>routes/</span><strong>Hono handlers</strong></li>
              <li><span>models/</span><strong>Drizzle schema</strong></li>
              <li><span>validation/</span><strong>Zod contracts</strong></li>
              <li><span>frontend/</span><strong>Typed SDK</strong></li>
            </ul>
          </div>
        </div>
      </div>
    </section>

    <section class="bp-targets" aria-labelledby="bp-targets-title">
      <div class="bp-shell bp-targets-layout">
        <div>
          <p class="bp-section-kicker">Choose the right output</p>
          <h2 id="bp-targets-title">One language, honest target status.</h2>
        </div>
        <ul class="bp-target-list">
          <li>
            <span class="bp-status-dot bp-status-complete" aria-hidden="true"></span>
            <div><strong>Node / Hono</strong><span>Mature reference</span></div>
          </li>
          <li>
            <span class="bp-status-dot bp-status-advanced" aria-hidden="true"></span>
            <div><strong>Python / FastAPI</strong><span>Advanced beta</span></div>
          </li>
          <li>
            <span class="bp-status-dot bp-status-experimental" aria-hidden="true"></span>
            <div><strong>Effect / TypeScript</strong><span>Experimental scaffold</span></div>
          </li>
        </ul>
        <a class="bp-text-link" href="/multi-target-codegen">
          Compare codegen targets
          <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10h11M11 6l4 4-4 4" /></svg>
        </a>
      </div>
    </section>

    <section class="bp-section bp-pipeline-section" aria-labelledby="bp-pipeline-title">
      <div class="bp-shell">
        <div class="bp-section-heading">
          <p class="bp-section-kicker">A compiler, not a black box</p>
          <h2 id="bp-pipeline-title">Every step has a clear job.</h2>
          <p>Your source is validated before generation, and the output is a standard project—not a proprietary runtime.</p>
        </div>
        <ol class="bp-pipeline" aria-label="Blueprint compiler pipeline">
          <li><span>01</span><strong>Source</strong><code>.bp</code></li>
          <li><span>02</span><strong>Parse</strong><code>AST</code></li>
          <li><span>03</span><strong>Check</strong><code>structure + names</code></li>
          <li><span>04</span><strong>Resolve</strong><code>semantic facts</code></li>
          <li><span>05</span><strong>Generate</strong><code>project files</code></li>
        </ol>
      </div>
    </section>

    <section class="bp-section bp-arrows-section" aria-labelledby="bp-arrows-title">
      <div class="bp-shell bp-arrows-layout">
        <div class="bp-section-heading bp-section-heading-sticky">
          <p class="bp-section-kicker">Flow you can scan</p>
          <h2 id="bp-arrows-title">Read the left margin. Understand the endpoint.</h2>
          <p>
            Inputs arrive, steps transform, outputs return. Intent is syntax—not a comment that drifts away from the code.
          </p>
          <a class="bp-text-link" href="/language-reference#_4-the-arrow-system">
            Learn the arrow system
            <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10h11M11 6l4 4-4 4" /></svg>
          </a>
        </div>
        <dl class="bp-arrow-list">
          <div>
            <dt><code>&lt;-</code><span>Input</span></dt>
            <dd><code>&lt;- email string required format(email)</code></dd>
          </div>
          <div>
            <dt><code>|&gt;</code><span>Step</span></dt>
            <dd><code>|&gt; user = fetch user(id)</code></dd>
          </div>
          <div>
            <dt><code>-&gt;</code><span>Output</span></dt>
            <dd><code>-&gt; 200 { id: user.id }</code></dd>
          </div>
          <div>
            <dt><code>@</code><span>Intent</span></dt>
            <dd><code>@ "Return the current user"</code></dd>
          </div>
          <div>
            <dt><code>@&gt;</code><span>Generation slot</span></dt>
            <dd><code>@&gt; "implement the scoring logic"</code></dd>
          </div>
        </dl>
      </div>
    </section>

    <section class="bp-section" aria-labelledby="bp-capabilities-title">
      <div class="bp-shell">
        <div class="bp-section-heading">
          <p class="bp-section-kicker">Useful from the first file</p>
          <h2 id="bp-capabilities-title">The service work is part of the language.</h2>
          <p>Model the contract once, then carry the same intent into validation, tests, documentation, and deployment.</p>
        </div>
        <div class="bp-capability-grid">
          <article class="bp-capability bp-capability-wide">
            <span class="bp-card-index">01 / HTTP + DATA</span>
            <h3>Typed endpoints and models</h3>
            <p>Declare request inputs, database models, validation, and response shapes in one readable flow.</p>
            <div class="bp-mini-schema" aria-hidden="true">
              <span><i>POST</i>/api/users</span>
              <span><i>201</i>{ id, email }</span>
            </div>
          </article>
          <article class="bp-capability">
            <span class="bp-card-index">02 / BOUNDARIES</span>
            <h3>Middleware, injected context, and secrets</h3>
            <p>Keep explicit security and cross-cutting behavior visible without burying the endpoint in framework plumbing.</p>
          </article>
          <article class="bp-capability">
            <span class="bp-card-index">03 / ASYNC</span>
            <h3>Workers, schedules, streams</h3>
            <p>Describe background and realtime work with the same directional syntax.</p>
          </article>
          <article class="bp-capability bp-capability-wide bp-capability-tools">
            <span class="bp-card-index">04 / TOOLCHAIN</span>
            <h3>Check, test, document, migrate, deploy</h3>
            <p>A single CLI carries the contract through the rest of the delivery loop.</p>
            <ul aria-label="Blueprint developer commands">
              <li><code>bp check</code></li>
              <li><code>bp test</code></li>
              <li><code>bp docs</code></li>
              <li><code>bp deploy</code></li>
            </ul>
          </article>
        </div>
      </div>
    </section>

    <section class="bp-section bp-workflow-section" aria-labelledby="bp-workflow-title">
      <div class="bp-shell">
        <div class="bp-section-heading">
          <p class="bp-section-kicker">A short path to running code</p>
          <h2 id="bp-workflow-title">Author. Check. Build. Own.</h2>
        </div>
        <ol class="bp-workflow">
          <li><span>01</span><div><h3>Author the contract</h3><p>Write models, endpoints, and intent in a compact <code>.bp</code> file.</p></div></li>
          <li><span>02</span><div><h3>Check before generating</h3><p>Catch syntax, naming, structural, and top-level reference problems with source-level diagnostics.</p></div></li>
          <li><span>03</span><div><h3>Build a familiar project</h3><p>Generate routes, schemas, tests, migrations, and project configuration.</p></div></li>
          <li><span>04</span><div><h3>Run it—or eject</h3><p>Keep compiling from Blueprint, or remove the markers and take full ownership.</p></div></li>
        </ol>
      </div>
    </section>

    <section class="bp-section bp-learning-section" aria-labelledby="bp-learning-title">
      <div class="bp-shell">
        <div class="bp-section-heading">
          <p class="bp-section-kicker">Choose your path</p>
          <h2 id="bp-learning-title">Start with the question you have.</h2>
        </div>
        <nav class="bp-learning-grid" aria-label="Blueprint learning paths">
          <a href="/getting-started">
            <span>Start</span><strong>Build a first service</strong><p>Install the CLI and reach a running endpoint.</p><i aria-hidden="true">01</i>
          </a>
          <a href="/examples">
            <span>Explore</span><strong>Read complete examples</strong><p>Move from hello world to richer service shapes.</p><i aria-hidden="true">02</i>
          </a>
          <a href="/language-reference">
            <span>Reference</span><strong>Learn every construct</strong><p>Find syntax, constraints, operations, and control flow.</p><i aria-hidden="true">03</i>
          </a>
          <a href="/architecture">
            <span>Contribute</span><strong>Understand the compiler</strong><p>Follow source through the Go toolchain and code generators.</p><i aria-hidden="true">04</i>
          </a>
        </nav>
      </div>
    </section>

    <section class="bp-final-cta" aria-labelledby="bp-final-title">
      <div class="bp-final-grid" aria-hidden="true"></div>
      <div class="bp-shell bp-final-layout">
        <div>
          <p class="bp-section-kicker">Your service can start as a readable contract</p>
          <h2 id="bp-final-title">Build the first endpoint in minutes.</h2>
        </div>
        <div class="bp-final-actions">
          <a class="bp-button bp-button-primary" href="/getting-started">
            Get started
            <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10h11M11 6l4 4-4 4" /></svg>
          </a>
          <a class="bp-text-link" href="https://github.com/abdul-hamid-achik/blueprint">
            View the repository
            <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10h11M11 6l4 4-4 4" /></svg>
          </a>
        </div>
      </div>
    </section>
  </main>
</template>
