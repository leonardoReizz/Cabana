import { Link } from 'react-router-dom'
import styles from './DocsPage.module.css'

/**
 * In-app documentation.
 *
 * Per CLAUDE.md, no user-facing feature is done until it is described here.
 * Everything on this page must reflect what the code actually does today —
 * planned behaviour belongs in ROADMAP.MD, not here.
 */

const SECTIONS = [
  { id: 'quickstart', label: 'Quickstart' },
  { id: 'requirements', label: 'App requirements' },
  { id: 'services', label: 'Services' },
  { id: 'build', label: 'Build variables' },
  { id: 'env', label: 'Environment' },
  { id: 'deploys', label: 'Deploys' },
  { id: 'scale', label: 'Scale and health' },
  { id: 'logs', label: 'Logs' },
  { id: 'domains', label: 'Domains' },
  { id: 'backups', label: 'Backups' },
  { id: 'limits', label: "What's not here yet" },
]

export function DocsPage() {
  return (
    <div className={styles.layout}>
      <nav className={styles.nav} aria-label="Documentation sections">
        {SECTIONS.map((s) => (
          <a key={s.id} href={`#${s.id}`} className={styles.navLink}>
            {s.label}
          </a>
        ))}
      </nav>

      <div className={styles.content}>
        <h1 className={styles.title}>Docs</h1>
        <p className={styles.lede}>
          cabana builds your app from a Git repository and runs it on your own
          machine. Everything below is what it does today.
        </p>

        <section id="quickstart" className={styles.section}>
          <h2>Quickstart</h2>
          <ol>
            <li>
              Go to <Link to="/apps">Apps</Link> and press{' '}
              <strong>New app</strong>. The setup walks through source, build
              variables, environment, and scale before anything is created.
            </li>
            <li>
              Give it a name — it becomes the subdomain, so{' '}
              <code>my-api</code> is served at <code>my-api.yourdomain.com</code>
              .
            </li>
            <li>
              Paste the HTTPS URL of a public Git repository that contains a{' '}
              <code>Dockerfile</code> at its root, and the branch to build.
            </li>
            <li>
              If your app inlines configuration at build time — anything{' '}
              <code>VITE_*</code>, <code>REACT_APP_*</code> or{' '}
              <code>NEXT_PUBLIC_*</code> — set it in the Build step. See{' '}
              <a href="#build">Build-time variables</a>.
            </li>
            <li>
              Press <strong>Deploy</strong> on the app page. The build log
              streams live under the <strong>Logs</strong> tab.
            </li>
          </ol>
          <p>
            A first deploy usually takes under a minute, most of it spent
            building your image. When it finishes, the hostname at the top of
            the app page becomes a link.
          </p>
        </section>

        <section id="requirements" className={styles.section}>
          <h2>App requirements</h2>
          <p>Three things, all of them checked at build or run time.</p>

          <h3>A Dockerfile at the repository root</h3>
          <p>
            cabana builds it inside the cluster with BuildKit. Multi-stage
            builds work, and so do minimal base images like{' '}
            <code>scratch</code>. There is no buildpack detection — no
            Dockerfile means no build.
          </p>

          <h3>Listen on port 8080</h3>
          <div className={styles.note}>
            <p>
              cabana injects <code>PORT=8080</code> and routes traffic there. It
              overrides any <code>PORT</code> you set yourself, because an app
              listening elsewhere would build, start, and never receive a
              request.
            </p>
          </div>
          <p>
            Read it from the environment rather than hardcoding, so the same
            image runs anywhere:
          </p>
          <pre className={styles.pre}>
            <code>{`const port = process.env.PORT || 8080
app.listen(port, '0.0.0.0')`}</code>
          </pre>
          <p>
            Bind to <code>0.0.0.0</code>, not <code>127.0.0.1</code>. An app
            bound to loopback is unreachable from outside its container.
          </p>

          <h3>A public HTTPS repository</h3>
          <p>
            Paste the URL as your browser shows it — cabana appends{' '}
            <code>.git</code> for you. SSH URLs and private repositories are not
            supported yet.
          </p>
        </section>

        <section id="services" className={styles.section}>
          <h2>Services</h2>
          <p>
            The <Link to="/services">Services</Link> page has a catalogue of
            databases, caches, search engines, message brokers and developer
            tools — Postgres, MySQL, MariaDB, MongoDB, Redis, Valkey, Memcached,
            Meilisearch, Qdrant, RabbitMQ, NATS, MinIO, Mailpit and Adminer.
            Press Install, give the instance a name, and cabana provisions it
            with generated credentials. The catalogue now also includes
            ClickHouse and Apache Kafka (which run as StatefulSets for stable
            storage and network identity) and Metabase (which comes with its own
            bundled Postgres, provisioned and wired up automatically).
          </p>

          <h3>Choosing a version</h3>
          <p>
            The install dialog has a <strong>Version</strong> field. Leave it
            empty to get the version cabana pins and tests, or type any tag
            published for that image on Docker Hub — <code>7.2-alpine</code>,{' '}
            <code>16</code>, <code>8.0.36</code>.
          </p>
          <div className={styles.note}>
            <p>
              Only the tag is yours to choose; the image itself always comes from
              the catalogue. Typing a version cannot install something other than
              the service you picked.
            </p>
          </div>
          <p>
            A tag that does not exist fails within seconds with the pull error,
            rather than hanging. The version cannot be changed after installing —
            install a second instance and migrate your data.
          </p>

          <h3>Linking a service to an app</h3>
          <p>
            On an app's page, pick the service from the Services panel and press
            Link. cabana injects the connection string as an environment
            variable — <code>REDIS_URL</code> for Redis,{' '}
            <code>DATABASE_URL</code> for PostgreSQL and MySQL, and so on. You
            never copy a password.
          </p>
          <div className={styles.note}>
            <p>
              Like every environment change, a link takes effect on the{' '}
              <strong>next deploy</strong>.
            </p>
          </div>
          <p>
            The injected variable is rendered from the service's live
            credentials each time you deploy, not copied into your environment
            when you link. That means unlinking removes it cleanly, and it
            cannot drift out of date. It also means a linked variable{' '}
            <strong>overrides</strong> one of the same name that you set by
            hand.
          </p>

          <h3>Service logs</h3>
          <p>
            A service's page has a log panel that shows the install as it
            happens — image pull, storage, rollout — and then the container's own
            output once it is running. When an install fails, the reason is
            there.
          </p>
          <p>
            Recent history is replayed when you open the page, so an install
            that finished while you were elsewhere is still readable.
          </p>

          <h3>Reach and safety</h3>
          <p>
            Services listen on the cluster network only — they are not reachable
            from the internet, which is deliberate for a database. Any app on
            this cabana can connect to any service, whether or not it is linked;
            linking is about injecting credentials, not about isolation.
          </p>
          <p>
            A service cannot be deleted while an app is linked to it. Unlink it
            first. Deleting a service <strong>destroys its data</strong>, and
            there is no backup yet.
          </p>
          <p>
            Some services keep nothing on disk by design — a cache like Redis or
            Memcached, a test mail catcher like Mailpit, a UI like Adminer. Those
            start empty after a restart, which is expected rather than a fault.
          </p>
        </section>

        <section id="build" className={styles.section}>
          <h2>Build-time variables</h2>
          <p>
            Some frameworks read their configuration while the bundle is being
            built, not when it runs. Vite (<code>VITE_*</code>), Create React App
            (<code>REACT_APP_*</code>) and static site generators all work this
            way: the value is inlined into the JavaScript and cannot be changed
            afterwards.
          </p>
          <p>
            Those belong in <strong>Build arguments</strong>, on the Settings
            tab. Anything set in Environment arrives too late — the bundle is
            already built.
          </p>

          <h3>Your Dockerfile needs the ARG line</h3>
          <p>
            A build argument only reaches your build if the Dockerfile declares
            it. Adding one in cabana without a matching{' '}
            <code>ARG</code> silently does nothing:
          </p>
          <pre className={styles.pre}>
            <code>{`FROM node:22-alpine AS build
WORKDIR /app

ARG VITE_API_URL
ENV VITE_API_URL=$VITE_API_URL

COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build`}</code>
          </pre>
          <p>
            The <code>ARG</code> receives the value; the <code>ENV</code> puts it
            where the build tool looks for it. Both lines are needed, and both
            must come before the build command.
          </p>

          <div className={styles.note}>
            <p>
              <strong>Build arguments are not secret.</strong> They are stored in
              the image and readable from its layer history by anyone who can
              pull it. Put API keys and passwords in Environment instead, which
              is stored as a Kubernetes Secret.
            </p>
          </div>

          <p>
            Changing a build argument requires a redeploy — it is baked in, so
            there is nothing to reload.
          </p>
        </section>

        <section id="env" className={styles.section}>
          <h2>Environment variables</h2>
          <p>
            The <strong>Environment</strong> panel on an app page is the full
            set: what you save is exactly what the app gets. Removing a row
            removes the variable.
          </p>
          <div className={styles.note}>
            <p>
              Saving does not restart your app. Changes take effect on the{' '}
              <strong>next deploy</strong>.
            </p>
          </div>
          <p>
            Names may contain letters, digits and underscores, and cannot start
            with a digit. Values are stored as Kubernetes Secrets and are never
            written to the logs.
          </p>
        </section>

        <section id="deploys" className={styles.section}>
          <h2>Deploys</h2>
          <p>Pressing Deploy runs four steps, all inside the cluster:</p>
          <ol>
            <li>BuildKit fetches your repository at the branch you chose.</li>
            <li>It builds your Dockerfile and pushes the image.</li>
            <li>cabana rolls out the new version alongside the old one.</li>
            <li>
              Once the new container accepts connections on 8080, traffic
              switches to it.
            </li>
          </ol>
          <p>
            Because the switch happens only after the new version answers,{' '}
            <strong>a failed build never takes your running app down</strong>.
            The deploy is marked failed and the previous version keeps serving.
          </p>
          <p>
            Every deploy is listed on the app page with its status and time.
            Each one is stored as a separate image, which is what will make
            one-click rollback possible later.
          </p>

          <h3>Status meanings</h3>
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>Status</th>
                  <th>Meaning</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>new</td>
                  <td>Created but never deployed.</td>
                </tr>
                <tr>
                  <td>building</td>
                  <td>Fetching the repository and building the image.</td>
                </tr>
                <tr>
                  <td>running</td>
                  <td>Serving traffic and answering health checks.</td>
                </tr>
                <tr>
                  <td>failed</td>
                  <td>
                    The build or the rollout failed. The log says which; any
                    previous version is still serving.
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section id="scale" className={styles.section}>
          <h2>Scale and health</h2>
          <p>
            An app's page is split into three tabs. <strong>Dashboard</strong>{' '}
            shows the live state of each replica, <strong>Logs</strong> holds
            output and deploy history, and <strong>Settings</strong> holds
            everything that changes how the app runs.
          </p>

          <h3>Replicas</h3>
          <p>
            The Settings tab sets how many copies of your app run at
            once, up to 20. Traffic is balanced across them, and more than one
            replica means a crash in one does not take the app down.
          </p>
          <div className={styles.note}>
            <p>
              Replicas share nothing. An app that keeps state in memory or on
              local disk will behave inconsistently above one replica.
            </p>
          </div>

          <h3>Health checks</h3>
          <p>
            <strong>TCP</strong> (the default) asks only whether something
            accepts a connection on 8080. <strong>HTTP</strong> additionally
            requires a 2xx from a path you choose, which catches an app that is
            listening but broken.
          </p>
          <p>
            The path must actually answer 2xx. If it does not, replicas never
            become ready, the deploy fails, and{' '}
            <strong>the previous version keeps serving</strong> — check the
            Replicas panel for the reason.
          </p>
          <p>
            <strong>Start delay</strong> is the grace period before the first
            check. Raise it for an app that takes a while to boot.
          </p>
          <p>
            <strong>Check every</strong> is the gap between checks. Three
            consecutive failures pull a replica out of the load balancer, so a
            10-second interval means a fault is acted on in about 30 seconds.
            Shorten it to react faster, lengthen it if your health endpoint is
            expensive to serve.
          </p>
          <p>
            Each check is given up to 5 seconds to answer, or the interval if
            that is shorter. This is deliberately more generous than Kubernetes'
            one-second default, which an endpoint that queries a database can
            exceed under load — failing a check for slowness rather than fault.
          </p>

          <h3>Zero-downtime deploys</h3>
          <p>
            New replicas start and pass their readiness check before any old one
            is retired. A failed deploy therefore never takes a working app
            down; you keep serving the previous version until a deploy succeeds.
          </p>

          <h3>Rollback</h3>
          <p>
            Every deploy keeps its image, so any succeeded deploy in the history
            has a <strong>Roll back</strong> button. Nothing is rebuilt, which
            makes it a matter of seconds.
          </p>
          <div className={styles.note}>
            <p>
              Rollback restores the <strong>image only</strong>. Environment
              variables, linked services, replica count and health check stay as
              they are now — if a bad env var is the problem, fix it there
              instead.
            </p>
          </div>

          <h3>Reading the Replicas panel</h3>
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>Status</th>
                  <th>Meaning</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>running</td>
                  <td>Serving traffic and passing its readiness check.</td>
                </tr>
                <tr>
                  <td>starting</td>
                  <td>
                    Up, but not yet passing readiness. Normal briefly after a
                    deploy; persistent means the check is wrong or the app is
                    slow.
                  </td>
                </tr>
                <tr>
                  <td>crashloop</td>
                  <td>
                    The container keeps exiting and being restarted. The Logs tab
                    says why.
                  </td>
                </tr>
                <tr>
                  <td>oom</td>
                  <td>
                    Killed for exceeding available memory. Reported even after
                    the restart makes it look healthy again.
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p>
            Restart counts are shown per replica. A rising count on an otherwise
            running app is the earliest sign something is wrong.
          </p>
        </section>

        <section id="logs" className={styles.section}>
          <h2>Logs</h2>
          <p>The log panel has two tabs, and they behave differently.</p>

          <h3>Logs</h3>
          <p>
            Your container's stdout and stderr, live, plus cabana's own notices
            about what it is doing to the app. This is a stream — it exists
            while the panel is open and is not stored.
          </p>
          <p>
            It follows the newest output automatically, unless you scroll up —
            then it holds your position until you scroll back down. Recent
            history is replayed when you open the page, and if your app restarts
            or you redeploy, the stream reattaches to the new container on its
            own.
          </p>

          <h3>Build</h3>
          <p>
            BuildKit's output for one deploy. Unlike the app log, this{' '}
            <strong>is stored</strong>: pick any deploy from the dropdown and
            read its build, including builds that failed weeks ago and builds
            from before cabana was last restarted.
          </p>
          <div className={styles.note}>
            <p>
              While a deploy is running the Build tab streams live. Once it
              finishes, it shows the stored copy instead.
            </p>
          </div>
          <p>
            Up to 1&nbsp;MB is kept per deploy. A build that produces more than
            that keeps its ending — the part that says what went wrong — and the
            panel tells you it was truncated. Build logs are deleted along with
            the app.
          </p>
        </section>

        <section id="domains" className={styles.section}>
          <h2>Domains</h2>
          <p>
            With a wildcard domain configured at install time, an app named{' '}
            <code>my-api</code> is served at <code>my-api.yourdomain.com</code>.
          </p>
          <p>
            Without one, cabana falls back to <code>sslip.io</code> — a hostname
            that encodes your server's IP address and resolves to it with no DNS
            setup at all. That looks like{' '}
            <code>my-api.203-0-113-10.sslip.io</code> and works immediately.
          </p>
          <h3>HTTPS</h3>
          <p>
            With a domain and an issuer configured at install time, cabana
            requests a certificate from Let's Encrypt for every app and renews
            it automatically. Nothing to do per app — deploying is enough.
          </p>
          <div className={styles.note}>
            <p>
              Certificates need a <strong>real domain</strong>. The sslip.io
              fallback is a shared domain whose Let's Encrypt rate limits are
              exhausted by everyone else using it, so cabana refuses to request
              certificates for it and serves plain HTTP instead.
            </p>
          </div>
          <p>
            Before switching a new domain to production certificates, install
            with <code>CABANA_TLS_ISSUER=letsencrypt-staging</code>. Its
            certificates are untrusted — your browser will warn — but its rate
            limit is generous, so a DNS or firewall problem costs nothing to
            discover. Let's Encrypt allows only five certificates per domain per
            week, which is easy to exhaust while debugging.
          </p>
          <p>
            For a certificate to be issued, the hostname must resolve publicly
            to this machine and port 80 must be reachable — that is how Let's
            Encrypt verifies you control the domain.
          </p>
        </section>

        <section id="backups" className={styles.section}>
          <h2>Backups</h2>
          <p>
            Set an S3-compatible destination once, under{' '}
            <Link to="/settings">Settings</Link> — Cloudflare R2, Backblaze B2,
            MinIO or AWS all work. After that, any database service has a{' '}
            <strong>Back up now</strong> button on its page.
          </p>
          <p>
            A backup is a logical dump (<code>pg_dump</code>,{' '}
            <code>mysqldump</code>, <code>mongodump</code>), not a raw file copy —
            the kind that reliably restores into a running database. It is taken
            by a short-lived job that never routes the data through cabana
            itself: the dump goes straight from the service to your bucket.
          </p>
          <div className={styles.note}>
            <p>
              <strong>Restore overwrites.</strong> It drops what is there and
              replaces it with the backup, so anything written since that backup
              is lost. The confirmation says so; there is no undo.
            </p>
          </div>
          <p>
            Only services that hold data offer backups — a cache or a UI has
            nothing to dump, so the panel does not appear for them.
          </p>

          <h3>Scheduling and retention</h3>
          <p>
            Each database service can back up <strong>daily</strong> or{' '}
            <strong>weekly</strong>, at an hour you choose (UTC), from the
            control on its page. Set <strong>Keep per service</strong> in
            Settings to prune automatically — beyond that count, the oldest
            backups are deleted from the bucket.
          </p>

          <h3>Recovering the whole platform</h3>
          <p>
            Settings also backs up <strong>cabana&rsquo;s own database</strong> —
            the record of every app, service, link and credential. That is the
            copy that survives losing the whole machine. To recover:
          </p>
          <ol>
            <li>Install cabana on the new host with the same domain.</li>
            <li>
              Point it at the same backup bucket in Settings.
            </li>
            <li>
              Restore the latest <code>cabana-state</code> dump into its database
              (documented in the README), then restart cabana.
            </li>
            <li>
              Re-provision the services it lists and restore each from its own
              latest backup.
            </li>
          </ol>
          <div className={styles.note}>
            <p>
              Restoring cabana&rsquo;s state is a deliberate, manual step — there
              is no one-click button for it, because it replaces the running
              platform&rsquo;s entire record of itself.
            </p>
          </div>
        </section>

        <section id="limits" className={styles.section}>
          <h2>What's not here yet</h2>
          <p>
            Known limits, so you find them here rather than by running into
            them:
          </p>
          <ul>
            <li>
              <strong>Public repositories only.</strong> No deploy keys, no SSH,
              no private repositories.
            </li>
            <li>
              <strong>No autoscaling.</strong> The replica count is whatever you
              set; it does not respond to load.
            </li>
            <li>
              <strong>No HTTPS.</strong> Apps are served over HTTP.
            </li>
            <li>
              <strong>Rollback restores the image only,</strong> not the
              configuration that was in effect at the time.
            </li>
            <li>
              <strong>Liveness is always TCP,</strong> whatever health check you
              configure. A user-supplied path on a probe that kills containers
              would turn one typo into an endless restart loop.
            </li>

            <li>
              <strong>No CPU or memory metrics.</strong>
            </li>
          </ul>
        </section>
      </div>
    </div>
  )
}
