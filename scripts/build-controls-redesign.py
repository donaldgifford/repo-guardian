#!/usr/bin/env python3
"""Build docs/design/controls-redesign.html from DESIGN-0029..0033.

Stdlib only (no markdown library is available on the dev machines). The
explainer sections are hand-written below; the five design documents are
converted from markdown and appended in full. Mermaid diagrams render
client-side from the mermaid CDN, so the page needs network access to draw.

Usage: python3 scripts/build-controls-redesign.py
Re-run after editing any of the five documents and commit the result.
"""
import glob
import html
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs/design/controls-redesign.html"
DOC_IDS = ["0029", "0030", "0031", "0032", "0033", "0034"]

# ----------------------------------------------------------------------------
# Minimal markdown → HTML
# ----------------------------------------------------------------------------

def slugify(s: str) -> str:
    s = re.sub(r"`", "", s)
    s = re.sub(r"[^\w\s-]", "", s.lower())
    return s.replace(" ", "-").replace("_", "-").strip("-")


def inline(s: str, prefix: str) -> str:
    codes: list[str] = []

    def stash(m):
        codes.append(html.escape(m.group(1).replace("\\|", "|")))
        return f"\x00{len(codes) - 1}\x00"

    s = re.sub(r"`([^`]+)`", stash, s)
    s = html.escape(s, quote=False).replace("\\|", "|")
    # links
    def link(m):
        text, href = m.group(1), m.group(2)
        if href.startswith("#"):
            href = f"#{prefix}-{href[1:]}"
        return f'<a href="{html.escape(href, quote=True)}">{text}</a>'

    s = re.sub(r"\[([^\]]+)\]\(([^)\s]+)\)", link, s)
    s = re.sub(r"\*\*(.+?)\*\*", r"<strong>\1</strong>", s)
    s = re.sub(r"(?<![\w*])\*(?!\s)(.+?)(?<!\s)\*(?![\w*])", r"<em>\1</em>", s)
    s = re.sub(r"\x00(\d+)\x00", lambda m: f"<code>{codes[int(m.group(1))]}</code>", s)
    return s


LIST_RE = re.compile(r"^(\s*)([-*]|\d+\.)\s+(.*)$")


def render_list(items: list[tuple[int, str, str]]) -> str:
    out: list[str] = []
    stack: list[tuple[int, str]] = []
    for indent, tag, text in items:
        if stack and indent > stack[-1][0]:
            stack.append((indent, tag))
            out.append(f"<{tag}><li>{text}")
            continue
        while stack and indent < stack[-1][0]:
            out.append(f"</li></{stack[-1][1]}>")
            stack.pop()
        if not stack:
            stack.append((indent, tag))
            out.append(f"<{tag}><li>{text}")
        elif stack[-1][1] != tag:
            out.append(f"</li></{stack[-1][1]}>")
            stack.pop()
            stack.append((indent, tag))
            out.append(f"<{tag}><li>{text}")
        else:
            out.append(f"</li><li>{text}")
    while stack:
        out.append(f"</li></{stack[-1][1]}>")
        stack.pop()
    return "".join(out)


def convert(md: str, prefix: str):
    lines = md.split("\n")
    # strip frontmatter
    if lines and lines[0].strip() == "---":
        j = next(k for k in range(1, len(lines)) if lines[k].strip() == "---")
        lines = lines[j + 1 :]
    out: list[str] = []
    headings: list[tuple[int, str, str]] = []
    i, n = 0, len(lines)
    while i < n:
        line = lines[i]
        s = line.strip()
        if s == "":
            i += 1
            continue
        if s.startswith("<!--"):
            while i < n and "-->" not in lines[i]:
                i += 1
            i += 1
            continue
        if s.startswith("```"):
            lang = s[3:].strip()
            body = []
            i += 1
            while i < n and not lines[i].strip().startswith("```"):
                body.append(lines[i])
                i += 1
            i += 1
            text = "\n".join(body)
            if lang == "mermaid":
                out.append(f'<pre class="mermaid">{html.escape(text, quote=False)}</pre>')
            elif lang == "gap":
                inner, _ = convert(text, prefix)
                out.append(f'<div class="note gap">{inner}</div>')
            else:
                cls = f' class="language-{html.escape(lang)}"' if lang else ""
                out.append(f"<pre><code{cls}>{html.escape(text, quote=False)}</code></pre>")
            continue
        m = re.match(r"^(#{1,6})\s+(.*)$", s)
        if m:
            level = len(m.group(1))
            text = m.group(2).strip()
            hid = f"{prefix}-{slugify(text)}"
            headings.append((level, text, hid))
            out.append(f'<h{level} id="{hid}">{inline(text, prefix)}</h{level}>')
            i += 1
            continue
        if s.startswith("|"):
            rows = []
            while i < n and lines[i].strip().startswith("|"):
                rows.append(lines[i].strip())
                i += 1
            def cells(r):
                r = r.strip()
                if r.startswith("|"):
                    r = r[1:]
                if r.endswith("|") and not r.endswith("\\|"):
                    r = r[:-1]
                return [c.strip() for c in re.split(r"(?<!\\)\|", r)]
            head = cells(rows[0])
            body_rows = [cells(r) for r in rows[2:]] if len(rows) > 1 and re.match(r"^\|?\s*:?-", rows[1]) else [cells(r) for r in rows[1:]]
            t = ["<table><thead><tr>"]
            t += [f"<th>{inline(c, prefix)}</th>" for c in head]
            t.append("</tr></thead><tbody>")
            for r in body_rows:
                t.append("<tr>" + "".join(f"<td>{inline(c, prefix)}</td>" for c in r) + "</tr>")
            t.append("</tbody></table>")
            out.append("".join(t))
            continue
        if LIST_RE.match(line):
            items: list[tuple[int, str, str]] = []
            while i < n:
                lm = LIST_RE.match(lines[i])
                if lm:
                    indent = len(lm.group(1).replace("\t", "    "))
                    tag = "ol" if lm.group(2)[0].isdigit() else "ul"
                    items.append((indent, tag, inline(lm.group(3), prefix)))
                    i += 1
                elif lines[i].strip() == "":
                    # blank line: list continues only if the next non-blank line is a list item
                    k = i + 1
                    while k < n and lines[k].strip() == "":
                        k += 1
                    if k < n and LIST_RE.match(lines[k]) and items:
                        i = k
                        continue
                    break
                elif lines[i].startswith("  ") or lines[i].startswith("\t"):
                    ind, tag, txt = items[-1]
                    items[-1] = (ind, tag, txt + " " + inline(lines[i].strip(), prefix))
                    i += 1
                else:
                    break
            out.append(render_list(items))
            continue
        if s.startswith(">"):
            q = []
            while i < n and lines[i].strip().startswith(">"):
                q.append(lines[i].strip()[1:].strip())
                i += 1
            out.append(f"<blockquote><p>{inline(' '.join(q), prefix)}</p></blockquote>")
            continue
        if s in ("---", "***"):
            out.append("<hr>")
            i += 1
            continue
        para = []
        while i < n:
            t = lines[i].strip()
            if t == "" or t.startswith("#") or t.startswith("|") or t.startswith("```") or LIST_RE.match(lines[i]) or t.startswith(">") or t.startswith("<!--"):
                break
            para.append(t)
            i += 1
        out.append(f"<p>{inline(' '.join(para), prefix)}</p>")
    return "\n".join(out), headings


# ----------------------------------------------------------------------------
# Extract decisions and open questions for the digest
# ----------------------------------------------------------------------------

def extract(md: str):
    decisions, oqs = [], []
    lines = md.split("\n")
    for k, line in enumerate(lines):
        m = re.match(r"^- \*\*(D\d+) (.+?)\*\*\s*(?:[—-]\s*)?(.*)$", line)
        if m:
            decisions.append((m.group(1), m.group(2).rstrip(".,"), m.group(3)))
        m = re.match(r"^### (OQ\d+): (.*)$", line)
        if m:
            rec, res = "", "open"
            for j in range(k + 1, min(k + 12, len(lines))):
                r = re.match(r"^- \(a\) ✅ recommended:\s*(.*)$", lines[j])
                if r:
                    rec = r.group(1)
                    break
                r = re.match(r"^\*\*(Resolved \d{4}-\d{2}-\d{2}: [^*]+)\*\*\s*(.*)$", lines[j])
                if r:
                    res = (r.group(1) + " " + r.group(2)).strip()
                r = re.match(r"^\*\*Open\.\*\*\s*(.*)$", lines[j])
                if r:
                    res = "open; " + r.group(1).split(":")[0].lower()
                r = re.match(r"^\*\*(Deferred[^*]*)\*\*\s*(.*)$", lines[j])
                if r:
                    res = r.group(1).strip()
            oqs.append((m.group(1), m.group(2), rec, res))
    return decisions, oqs


DISPOSITIONS = ("Accepted", "Accepted with changes", "Rejected", "Deferred")


def extract_reviews(
    md: str,
    headings: list[tuple[int, str, str]],
) -> list[tuple[str, str, str, str, str]]:
    """Extract finding IDs, severities, titles, anchors and dispositions.

    The disposition comes from the ``**Response:** **<Disposition>.**``
    paragraph the design owner writes under each finding; a finding without
    one is reported as ``pending``.
    """
    reviews = []
    for level, text, anchor in headings:
        match = re.fullmatch(
            r"(AR-\d{4}-\d{2}) \((critical|high|medium)\): (.+)", text
        )
        if level != 3 or not match:
            continue
        finding_id = match.group(1)
        section = re.search(
            rf"^### {re.escape(finding_id)} .*?(?=^### |^## |\Z)",
            md, re.M | re.S,
        )
        disposition = "pending"
        if section:
            resp = re.search(
                r"\*\*Response:\*\* \*\*(" + "|".join(DISPOSITIONS) + r")\.\*\*",
                section.group(0),
            )
            if resp:
                disposition = resp.group(1)
        reviews.append((*match.groups(), anchor, disposition))
    return reviews


def render_reviews(docs: list[dict]) -> str:
    """Build the review overview directly from the design findings."""
    counts = {
        severity: sum(
            finding[1] == severity
            for doc in docs
            for finding in doc["reviews"]
        )
        for severity in ("critical", "high", "medium")
    }
    total = sum(counts.values())
    responses = {
        d: sum(
            finding[4] == d
            for doc in docs
            for finding in doc["reviews"]
        )
        for d in (*DISPOSITIONS, "pending")
    }
    response_summary = ", ".join(
        f"{n} {d.lower()}" for d, n in responses.items() if n
    )
    parts = [
        '<hr class="docsep"><section id="review">',
        '<h1>Adversarial review</h1>',
        '<p class="lede">Counterexamples, race conditions and contract '
        'contradictions across DESIGN-0029 to DESIGN-0033.</p>',
        '<p class="meta">Reviewed 2026-10-03; responded 2026-10-03; applied 2026-10-04. Each '
        'finding in its source design records the challenged contract, '
        'failure scenario, proposed correction and verification case, '
        'followed by the design owners\' <strong>Response</strong> giving '
        'the disposition and the concrete change, and an <strong>Applied</strong> line naming where the change landed in the body or the Decisions ledger.</p>',
        '<p class="note"><strong>Review disposition: changes required before '
        'implementation of the affected contracts.</strong> '
        f'<strong>Response: {response_summary}.</strong> The accepted changes '
        'were applied to the design bodies and Decisions ledgers on 2026-10-04, '
        'so the designs are authoritative. The explainer and deep dives above '
        'describe the guarantees as first proposed; where they differ from a '
        'design body, the body wins.</p>',
        f'<p><strong>{total} findings:</strong> '
        f'{counts["critical"]} critical, {counts["high"]} high, '
        f'{counts["medium"]} medium. Critical findings contradict a core '
        'safety guarantee; high findings block correctness or operation; '
        'medium findings need an explicit contract decision.</p>',
        '<p>The critical CODEOWNERS ordering counterexample now belongs to the deferred ownership design (DESIGN-0034); this version\'s codeowners control is existence plus template. '
        'The recurring themes are assignment and operation fencing, '
        'ownership versus read dependencies, lifecycle cleanup independent '
        'of remediation eligibility, measurement coverage, and shared '
        'schema provenance. The index is generated from the source '
        'documents so finding IDs and links stay in sync.</p>',
    ]
    for doc in docs:
        did = doc["id"]
        parts.append(
            f'<h2 id="review-{did}">DESIGN-{did}</h2>'
            f'<p><a href="#d{did}-adversarial-review">'
            'Read this design\'s full review</a></p>'
            '<table><thead><tr><th>Finding</th><th>Severity</th>'
            '<th>Challenged contract</th><th>Response</th></tr></thead><tbody>'
        )
        for finding_id, severity, title, anchor, disposition in doc["reviews"]:
            cls = "disp-" + disposition.lower().replace(" ", "-")
            parts.append(
                f'<tr><td><a href="#{anchor}">{finding_id}</a></td>'
                f'<td>{severity}</td><td>{inline(title, "d" + did)}</td>'
                f'<td><span class="disp {cls}">{disposition}</span></td></tr>'
            )
        parts.append('</tbody></table>')
    parts.append('</section>')
    return ''.join(parts)


# ----------------------------------------------------------------------------
# Explainer content (hand-written)
# ----------------------------------------------------------------------------

ARCH = """flowchart LR
    subgraph GitHub
        GH[("Repositories")]
        EA[["Evaluation App<br/>read-only"]]
        RA[["Remediation App<br/>read and write"]]
    end
    subgraph Policy
        CAT["Control catalogue"]
        ENT["Enterprise policy"]
        ORG["Org policies"]
    end
    subgraph Temporal["Temporal control plane"]
        DISC["Discovery"]
        RES["Resolve assignments<br/>policy + repository state + App installations"]
        EVAL["Evaluation workflow per repository<br/>evaluator role, queue repo-guardian-eval"]
        REM["Remediation workflow per repository and control<br/>remediator role, queue repo-guardian-remediate"]
        BE["rate budget<br/>installation/eval/{id}"]
        BR["rate budget<br/>installation/remediate/{id}"]
    end
    DB[("Postgres<br/>assignments · results ·<br/>generations · remediations")]
    API["API + UI"]
    GH -- "all webhooks" --> EA
    EA --> DISC
    EA --> EVAL
    CAT --> RES
    ENT --> RES
    ORG --> RES
    DISC --> RES --> DB
    EVAL --> BE
    EVAL -- "read via Evaluation App" --> GH
    EVAL --> DB
    EVAL -- "changed and mode = remediate" --> REM
    REM --> BR
    REM -- "write via Remediation App" --> GH
    REM --> DB
    DB --> API"""

LIFE = """sequenceDiagram
    autonumber
    participant D as Discovery
    participant E as Evaluation
    participant DB as Postgres
    participant R as Remediation
    participant GH as GitHub
    D->>DB: repository discovered and active, controls resolved from policy, repository state and App installations
    D->>E: start evaluation
    E->>GH: read default branch (Evaluation App)
    E->>DB: CODEOWNERS 1.0 non_compliant (1.1 fail), first evaluation inserts generation 1
    E->>R: changed, mode = remediate
    R->>GH: branch repo-guardian/codeowners, full template, open PR (Remediation App)
    R->>DB: PR #12 open, remediated generation 1
    Note over E: next scheduled evaluation
    E->>GH: read default branch
    E->>DB: same fingerprint, evaluated_at only, generation stays 1
    Note over R: not signalled, nothing changed
    GH-->>E: PR #12 merged (webhook to the Evaluation App)
    E->>GH: read default branch
    E->>DB: CODEOWNERS 1.0 compliant, generation 2, PR #12 merged"""

V1_VS_NEW = """flowchart LR
    subgraph V1["v1 today: rules in isolation"]
        R1["rule codeowners (exists)"] --> F1["CODEOWNERS"]
        R2["rule wiz-owners (contains)"] --> F1
        R3["orphan cleanup"] -. "deletes what another rule still wants" .-> F1
    end
    subgraph NEW["controls: one owner per resource"]
        C1["control codeowners@1<br/>rule: exists; ownership rules deferred to DESIGN-0034"] --> F2["CODEOWNERS"]
        C1 -- "one Evaluate, one Remediate,<br/>one PR" --> P2["PR repo-guardian/codeowners"]
    end"""

BIG_CHANGES = [
    (
        "Controls replace rules",
        "Generic file/setting/branch-protection rules, each owning a path and run in isolation; a reconciler registry bolted on after the check.",
        "A <em>control</em> is an opinionated, versioned requirement implemented once in Go (<code>codeowners</code>, <code>catalog_info</code>, <code>dependency_updates</code>, settings, rulesets, labels, properties) with its own control rules. A control declares the resources it owns; two active controls can never own one resource, enforced at policy load. Every check on a file lives inside one control, so there is nothing to conflict.",
        "Patching the orphan-cleanup bug fixes one symptom of a structural problem — INV-level review found nine of the same shape. A plan/apply layer over the old rules was drafted and dropped: it adds machinery without removing the conflict. The generic <code>file</code> control survives only as an escape hatch (exists or exact, nothing in between).",
    ),
    (
        "Policy is who and what: enterprise plus org, no repo policy type",
        "One flat HCL with per-rule <code>scope</code> and <code>ignore</code> blocks, strict mode vs legacy mode, and no provenance for why a rule applied.",
        "An <strong>enterprise policy</strong> lists the orgs (the onboarding gate) and the baseline controls, with no excludes. An <strong>org policy</strong> adds, replaces, excludes (with a required reason), carves out repositories by name glob and sets the mode. Resolution is layered — a later layer can re-add what an earlier one excluded — and every assignment records which layer decided it.",
        "A third repo-level policy type was the brief's own caveat; <code>repos { match = [...] }</code> blocks inside the org policy give the same precision without a third file to reason about (DESIGN-0030 OQ1 keeps the door open). Fact-based selectors (topics, visibility) were deliberately left out; name globs are enough today and fwsync's facets are the obvious shape if that changes.",
    ),
    (
        "Evaluation is split from remediation, on two GitHub Apps",
        "One App with write permission does everything; the engine checks and writes in one pass.",
        "An <strong>Evaluation App</strong> (read-only) evaluates every managed repository on a schedule and on push, always against the default branch, and writes per-control status, fingerprint and evidence. A <strong>Remediation App</strong> (write) opens PRs only when asked. Each App has its own installation, rate budget, Temporal task queue and worker role, so the two scale independently.",
        "Least privilege is the obvious win; the operational one is that the fleet can run in <code>evaluate</code> mode for months before any PR is opened, so compliance numbers exist before remediation is switched on per org. One App could do both with a flag, but then a leaked evaluator key is a write key and the budgets are shared.",
    ),
    (
        "Remediation runs only when the evaluation changed",
        "Every sweep re-reconciles every repository with an open PR, refreshing bodies and comments whether or not anything moved.",
        "Each (repository, control) carries an <code>eval_generation</code> that increments when the fingerprint changes and a <code>remediated_generation</code> the remediator records. Remediation is due when eval is ahead, when a human edited the PR (<code>pr_changed</code>), or when a human closed it and the cooldown passed. The first evaluation inserts generation 1, so onboarding remediates immediately; the second identical evaluation only touches <code>evaluated_at</code>.",
        "The brief proposed a boolean <code>change_since_last_eval</code>; two evaluations before one remediation would reset it and lose the change. Counters are monotonic, race-safe (the remediator records the generation it read at start) and cheap. Always-remediate was the v1 behaviour that produced the noise.",
    ),
    (
        "One PR per control, with a lifecycle",
        "One branch per repository (<code>repo-guardian/add-missing-files</code>) bundling every rule, with orphan cleanup deleting files from it as rules drop out.",
        "Each control gets <code>repo-guardian/&lt;control slug&gt;</code>. A PR is <code>open → merged</code>, <code>open → closed_compliant</code> (main became compliant, repo-guardian closes it) or <code>open → closed_by_user</code> (a new PR after the cooldown). The remediator adopts a branch only when every commit is its own; a branch with foreign commits is left alone and recorded as a hold.",
        "The single bundle is where the shared-file deletion came from. Per-control PRs keep each review small and let one control's PR merge while another's waits. A per-repository cap on open PRs (default 3) bounds the noise on a badly non-compliant repository — the cap is DESIGN-0032 OQ2.",
    ),
    (
        "Resolution reads stored state; parking stays",
        "Scope/ignore gates evaluated per rule per check; archived and fork repositories parked by the worker, un-parked only by discovery.",
        "Resolution runs at discovery, makes no API calls, and takes three inputs: the policy snapshot, the repository row (<code>active</code>, archived, fork) and each App's installation status for the org. A parked repository resolves to no assignments and is never scanned; if the Remediation App is not installed, every assignment is forced to <code>evaluate</code> with a persisted reason.",
        "The parking mechanism is the reason archived repositories are never scanned today; dropping it for a &ldquo;pure&rdquo; resolver would have re-scanned them. Persisting the reason (<code>mode_reason</code>, <code>repository_policy_state</code>) is what lets the UI say <em>why</em> a repository or control is not being remediated.",
    ),
    (
        "Fewer open questions, decisions on record",
        "32 open questions across the first draft, most already assumed by the body text.",
        "11 open questions remained in the four core docs (plus 4 in the fwsync companion) after the first rework; everything else became a numbered Decision with a one-line rationale. On 2026-10-04 the maintainer answered fourteen of the fifteen, each now a Decision in its document (DESIGN-0029 D4–D5, DESIGN-0030 D8–D11, DESIGN-0031 D9–D11, DESIGN-0032 D11–D14, DESIGN-0033 D6–D7), and deferred the fifteenth: CODEOWNERS ownership semantics moved to DESIGN-0034, undecided, while this version's codeowners control is existence plus template.",
        "A question whose answer the body already depends on is not a question; it is an unflagged decision. Recording them lets a reviewer disagree with a specific line instead of re-deriving the design.",
    ),
    (
        "fwsync: borrow conventions, not code, share nothing",
        "Two tools (fwsync for Wiz, repo-guardian for GitHub) with similar HCL vocabularies and no stated relationship.",
        "Seven fwsync conventions are adopted into the design (documents-are-data decoding, slug grammar, plan-as-data with exit codes, lock-bounded action, drift verdicts, fail-safe fact lookup, its test kit). Every way of <em>using</em> fwsync is rejected — it is <code>internal/</code>-only, Wiz-typed, has no JSON output and has never been applied live. The governed tag schema was evaluated as a shared artifact and rejected on 2026-10-04 (DESIGN-0033 D6): repo-guardian declares its own property names and value rules, the built-in keys stay <code>Owner</code>/<code>Component</code>, and the only relationship allowed is fwsync generating repo-guardian policy in repo-guardian's documented format.",
        "Importing or forking a Draft, never-deployed tool pointed at a different control plane buys nothing. Two definitions of the same GitHub properties looked like a defect to share away; the maintainer's call is that they are two tools' definitions, designed closely and kept separate, because fwsync is private and tying repo-guardian's catalogue to a schema it does not own would put another tool's revision history inside repo-guardian's policy version.",
    ),
]

KEEP_GO = {
    "Kept (assumptions register, verified against the v2 code in INV-0021)": [
        "Temporal control plane: <code>RepoWorkflow</code> shape, signals, ContinueAsNew; the rate-budget <code>InstallationWorkflow</code> (gains an App dimension in its id); discovery and policy-rollout workflows",
        "Repository identity (<code>repositories.id</code>, rename/transfer events) and parking via <code>UpsertDiscovered</code> as the only un-parker",
        "Ingest role and HMAC validation; the API role, OIDC authn/authz, read-only pool and scoped queries; the UI",
        "The <code>VersionV2</code> pattern (classified-field hash with its every-field-classified test) and the compliance snapshot query shape",
        "The catalog-info parser, the template renderer with curated helpers, the GitHub client transport chain (otelhttp → rate limit → ghinstallation)",
    ],
    "Lifted or adapted (the INV-0021 caveats)": [
        "<code>UpsertRepositories</code> gains a resolution step between upsert and SignalWithStart; the rollout re-resolves before it re-signals (A3, A7)",
        "Discovery, ingest, config and the chart model one App today and gain an App dimension: per-App access tables, two webhook secrets selected by target App id, two credential blocks (A3, A13, A15)",
        "Five checker pieces are lifted, not deleted: durable-skip classification, identity capture, the setting/ruleset/label comparators, foreign-PR matching, the E4-locked log lines (A8)",
        "<code>control.Reader</code>/<code>PRObserver</code>/<code>Writer</code> need client methods that do not exist yet (ref-pinned reads, tree listing, git-data commit); the <code>VersionV2</code> input is rewritten wholesale (A7, A12)",
        "A posture exporter over control results is new work: no v2 role exports posture today (A19); the catalog parser must expose the entity, not only the properties projection (A11)",
    ],
    "Replaced": [
        "The checker engine's two-pass rule iteration, gate evaluator, orphan cleanup and inverse-orphan restoration → <code>Control.Evaluate</code> / <code>Control.Remediate</code> per control",
        "The reconciler registry (custom_properties, label_sync, branch_protection, workflow_sync) → control types with declared resources; <code>workflow_sync</code> dissolves into push-path scoping",
        "<code>rule_state</code> / findings keyed by (kind, name) → <code>control_results</code>, <code>rule_results</code>, <code>result_events</code>, <code>remediations</code> with generations",
        "The single reconcile branch and sticky reconcile-log comment → per-control branches and PR lifecycle states",
        "Per-rule <code>scope</code> / <code>ignore</code> blocks and strict/legacy scope modes → enterprise + org policies with layered resolution and provenance",
    ],
}

READING_ORDER = [
    ("0029", "Overview — vocabulary, architecture, lifecycle, v1 mapping, the assumptions register (A1–A23), risks"),
    ("0030", "Policy model — catalogue, enterprise and org policies, resolution, modes, ownership, data model"),
    ("0031", "Control framework — the Go interfaces and types, rule results, file controls, the built-in types, conformance suite"),
    ("0032", "Evaluation and remediation — the two Apps, change detection, PR lifecycle, schema, API, metrics, failure semantics, Temporal mapping"),
    ("0033", "fwsync companion — concept mapping, what transfers, reuse options; nothing shared"),
    ("0034", "CODEOWNERS ownership control — deferred and undecided; read only when a team needs ownership enforcement"),
]

CSS = """
:root{--bg:#0f1218;--panel:#161b24;--panel2:#1c2330;--border:#263040;--deep:#0b0e13;--fg:#d8dee9;--muted:#8b96a8;--dim:#5d6778;
--blue:#6cb6ff;--green:#5fd38d;--amber:#f0b84a;--red:#f07178;--purple:#c792ea;--cyan:#5ccfe6;
--line:var(--border);--accent:var(--blue);--soft:var(--panel);--side:300px}
*{box-sizing:border-box}
html{scroll-behavior:smooth}
body{margin:0;font:15.5px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Inter,Roboto,sans-serif;color:var(--fg);background:var(--bg)}
nav#side{position:fixed;top:0;left:0;bottom:0;width:var(--side);overflow:auto;border-right:1px solid var(--border);background:#0c0f14;padding:18px 14px;font-size:13px}
nav#side h1{font-size:14px;margin:0 0 4px;color:var(--fg)}
nav#side .sub{color:var(--muted);margin-bottom:14px;font-size:12px}
nav#side a{color:var(--muted);text-decoration:none;display:block;padding:3px 8px;border-radius:6px;border-left:2px solid transparent}
nav#side a:hover{color:var(--fg);background:var(--panel)}
nav#side .doc{margin-top:12px;font-weight:600;color:var(--fg)}
nav#side .h3{padding-left:20px;font-size:12.5px}
main{margin-left:var(--side);padding:32px 48px 120px;max-width:1120px}
h1{font-size:30px;line-height:1.25;margin:0 0 6px}
h2{font-size:24px;margin:48px 0 12px;padding-bottom:6px;border-bottom:1px solid var(--border)}
h3{font-size:19px;margin:30px 0 8px}
h4{font-size:16px;margin:22px 0 6px}
p{margin:10px 0}
a{color:var(--blue)}
code{font:12.5px/1.4 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;background:var(--deep);border:1px solid var(--border);padding:0 4px;border-radius:4px}
pre{background:var(--deep);border:1px solid var(--border);border-radius:8px;padding:12px 14px;overflow:auto;font-size:12.5px;line-height:1.45}
pre code{background:none;border:0;padding:0}
pre.mermaid{background:var(--deep);text-align:center}
table{border-collapse:collapse;width:100%;margin:12px 0;font-size:13.5px}
th,td{border-bottom:1px solid var(--border);padding:7px 10px;vertical-align:top;text-align:left}
th{color:var(--muted);font-weight:600;font-size:12px;text-transform:uppercase;letter-spacing:.04em;background:#121720}
tr:hover td{background:#141a23}
blockquote{margin:12px 0;padding:8px 16px;border-left:3px solid var(--border);color:var(--muted)}
.lede{font-size:17px;color:var(--muted);margin:0 0 20px;max-width:900px}
.meta{font-size:13px;color:var(--muted);margin-bottom:28px}
.hero{padding:26px 28px;border:1px solid var(--border);border-radius:14px;background:linear-gradient(135deg,#17202d,#121722 60%,#1a1726);margin-bottom:36px}
.hero h1{font-size:32px;margin:0 0 8px}
.hero .lede{margin-bottom:10px}
.hero .meta{margin-bottom:0}
.card{background:var(--panel);border:1px solid var(--border);border-radius:12px;padding:16px 20px;margin:14px 0}
.card h4{margin:0 0 8px;font-size:16px}
.card .row{display:grid;grid-template-columns:120px 1fr;gap:6px 14px;font-size:14px}
.card .k{color:var(--muted);font-weight:600}
.docsep{margin:72px 0 24px;border:0;border-top:3px double var(--border)}
.doc-title{font-size:26px;margin:0 0 4px}
.oq-rec{background:#11201a;color:var(--green);border:1px solid #285a3d;padding:1px 7px;border-radius:999px;font-size:12.5px}
.note{border-left:3px solid var(--amber);background:#1d1a12;padding:10px 14px;border-radius:0 8px 8px 0;font-size:14px;margin:14px 0}
.note p{margin:6px 0}.note ul{margin:6px 0 0 18px;padding:0}.note li{margin:4px 0}
.disp{display:inline-block;padding:1px 9px;border-radius:999px;font-size:11.5px;font-weight:600;white-space:nowrap;border:1px solid var(--border);background:var(--panel)}
.disp-accepted{color:var(--green);border-color:#285a3d}
.disp-accepted-with-changes{color:var(--blue);border-color:#2b4a6b}
.disp-rejected{color:var(--red);border-color:#5f2a30}
.disp-deferred{color:var(--amber);border-color:#5f4a20}
.disp-pending{color:var(--muted);border-color:#3a4a60}
pre code.hljs{background:transparent;padding:0}
.kv{display:grid;grid-template-columns:1fr 1fr;gap:16px}
.kv ul{margin:6px 0 0 18px;padding:0}
hr{border:0;border-top:1px solid var(--border)}
@media (max-width:1100px){nav#side{display:none}main{margin-left:0;padding:24px}}
@media print{nav#side{display:none}main{margin:0;max-width:none;color:#000;background:#fff}h2{page-break-before:always}pre{white-space:pre-wrap}}
"""


def main():
    docs = []
    for did in DOC_IDS:
        p = glob.glob(str(ROOT / f"docs/design/{did}-*.md"))[0]
        md = Path(p).read_text()
        title = re.search(r'^title:\s*"(.*)"', md, re.M).group(1)
        body, headings = convert(md, f"d{did}")
        decisions, oqs = extract(md)
        docs.append(dict(
            id=did,
            title=title,
            html=body,
            headings=headings,
            decisions=decisions,
            oqs=oqs,
            reviews=extract_reviews(md, headings),
            path=Path(p).name,
        ))

    deep_md = (Path(__file__).with_name("controls-redesign-deep-dives.md")).read_text()
    deep_html, deep_headings = convert(deep_md, "dd")

    # ---- sidebar
    side = ['<nav id="side"><h1>repo-guardian controls redesign</h1><div class="sub">DESIGN-0029 → 0034 · branch docs/controls-and-policies · PR #202</div>']
    side.append('<a class="doc" href="#explainer">Explainer</a>')
    for anchor, label in [("why", "The problem"), ("changes", "The big changes"), ("arch", "Architecture"), ("life", "A repository's life"), ("keep", "What stays, what goes"), ("fwsync", "fwsync"), ("ledger", "Decision ledger"), ("oqs", "Open questions"), ("read", "Reading order")]:
        side.append(f'<a class="h3" href="#x-{anchor}">{label}</a>')
    side.append('<a class="doc" href="#review">Adversarial review</a>')
    for d in docs:
        side.append(
            f'<a class="h3" href="#review-{d["id"]}">'
            f'DESIGN-{d["id"]} findings</a>'
        )
    side.append('<a class="doc" href="#deep">Deep dives</a>')
    for level, text, hid in deep_headings:
        if level == 2:
            side.append(f'<a class="h3" href="#{hid}">{html.escape(re.sub("`", "", text))}</a>')
    for d in docs:
        side.append(f'<a class="doc" href="#d{d["id"]}">DESIGN-{d["id"]}</a>')
        for level, text, hid in d["headings"]:
            if level == 2:
                side.append(f'<a class="h3" href="#{hid}">{html.escape(re.sub("`", "", text))}</a>')
    side.append("</nav>")

    # ---- explainer
    x = []
    x.append('<section id="explainer">')
    x.append('<div class="hero">')
    x.append("<h1>repo-guardian controls redesign</h1>")
    x.append('<p class="lede">Opinionated controls assigned by enterprise and org policies, evaluated read-only against every repository, remediated by one PR per control only when something changed.</p>')
    x.append('<p class="meta">DESIGN-0029, 0030, 0031, 0032, the fwsync companion DESIGN-0033 and the deferred DESIGN-0034 · Draft · 2026-10-02 · the four deep dives (data model, API, database, implementation order) and then all five documents follow in full. Reconciled 2026-10-03: every gap the deep dives found is now a numbered decision in its design. Adversarially reviewed and responded 2026-10-03. Open questions answered 2026-10-04: fourteen of fifteen resolved, CODEOWNERS ownership deferred to DESIGN-0034. Review responses applied to the bodies and Decisions ledgers 2026-10-04; the assumptions register (A1–A23) audited against the v2 code in INV-0021 the same day, and its three questions answered 2026-10-05 (fresh database at cutover, no in-place upgrade path). An interactive walkthrough of the changes, trade-offs, open questions and Temporal impact lives in <a href="controls-walkthrough.html">controls-walkthrough.html</a>. Diagrams render with mermaid from a CDN; open with network access.</p>')
    x.append('</div>')
    x.append(
        '<p class="note">The <a href="#review">2026-10-03 adversarial '
        'review</a> adds findings to every design, each answered by a '
        'design-owner response with its disposition. Read it alongside the '
        'proposed guarantees in this explainer and the deep dives; the '
        'accepted changes were applied to the design bodies on 2026-10-04, '
        'so where this page and a design differ, the design wins.</p>'
    )

    x.append('<h2 id="x-why">The problem</h2>')
    x.append("<p>v1 evaluates each rule in isolation against the files it names. Rules that target the same file have no shared view of the result: one rule's cleanup deleted a CODEOWNERS file another rule still required (the wiz-owners incident on v1.11.1), two rules could write one file in turn, and a &ldquo;must exist&rdquo; / &ldquo;must not exist&rdquo; pair on the same path looped forever. A review of the engine found nine places with the same shape. The conclusion was that the engine is too generic &mdash; a rule is a path plus a check mode, and nothing in the model knows that four of those rules are all <em>about CODEOWNERS</em>. The fix is structural, not another patch.</p>")
    x.append(f'<pre class="mermaid">{html.escape(V1_VS_NEW, quote=False)}</pre>')

    x.append('<h2 id="x-changes">The big changes</h2>')
    x.append("<p>Each card: what changes, what it replaces, why, and why not the alternatives that were considered.</p>")
    for i, (title, replaces, why, whynot) in enumerate(BIG_CHANGES, 1):
        x.append(f'<div class="card"><h4>{i}. {title}</h4><div class="row">')
        x.append(f'<div class="k">Replaces</div><div>{replaces}</div>')
        x.append(f'<div class="k">What</div><div>{why}</div>')
        x.append(f'<div class="k">Why not</div><div>{whynot}</div>')
        x.append("</div></div>")

    x.append('<h2 id="x-arch">Architecture</h2>')
    x.append(f'<pre class="mermaid">{html.escape(ARCH, quote=False)}</pre>')
    x.append("<ul>")
    for t in [
        "<strong>Two Apps, two of everything that scales.</strong> Separate installations, rate budgets, task queues and worker roles. Only the Evaluation App receives webhooks, including <code>pull_request</code> events for PRs the Remediation App opened.",
        "<strong>Resolution reads stored state only.</strong> Policy snapshot + repository row + App installations → assignments with provenance. No API calls. Parked repositories get nothing.",
        "<strong>Evaluation is idempotent and always against the default branch</strong> at a pinned SHA, through one cached reader, writing results, evidence, fingerprints and generations in one transaction.",
        "<strong>Remediation is a workflow per (repository, control)</strong>, signal-with-started by evaluation, with an hourly sweep as the backstop. Nothing blocks in-handler; throttles defer.",
        "<strong>Posture is read back from Postgres</strong>, never counted at check time; the API and UI read the same rows the report and snapshots do.",
    ]:
        x.append(f"<li>{t}</li>")
    x.append("</ul>")

    x.append('<h2 id="x-life">A repository\'s life</h2>')
    x.append("<p>The CODEOWNERS example from the brief: discovered, evaluated, remediated, re-evaluated without re-remediating, merged.</p>")
    x.append(f'<pre class="mermaid">{html.escape(LIFE, quote=False)}</pre>')

    x.append('<h2 id="x-keep">What stays, what goes</h2>')
    x.append('<div class="kv">')
    for k, items in KEEP_GO.items():
        x.append(f"<div><strong>{k}</strong><ul>" + "".join(f"<li>{t}</li>" for t in items) + "</ul></div>")
    x.append("</div>")
    x.append('<p class="note">The keep/replace split rests on the assumptions register in DESIGN-0029 (A1–A23), verified against the v2 code in INV-0021 on 2026-10-04: ten hold, ten hold with a caveat that changes the plan but not the model, three (A21–A23) are GitHub facts for a homelab probe, none fails. The F1–F10 register of DESIGN-0033 is mostly moot after its D6. The three open questions of INV-0021 were answered on 2026-10-05: suspension is access state, never a park (0029 D6, 0030 D6); <code>repositories.installation_id</code> is dropped (0030 D19); v2.0.0 starts on a fresh database with no in-place upgrade path (0029 D11, 0032 D27).</p>')

    x.append('<h2 id="x-fwsync">fwsync</h2>')
    x.append("<p>fwsync is the maintainer's Wiz-side tool: frameworks, rules, scan policies and projects as HCL in git, compiled from a compact domain schema and diff-applied to the Wiz API with a lockfile. Its vocabulary rhymes with the controls model and its direction is the opposite &mdash; it owns its target, repo-guardian proposes. DESIGN-0033 maps every concept, adopts seven conventions and its test kit, rejects every form of code reuse with evidence from the source, and evaluated the one apparent seam, the governed tag schema both tools would use to define the same GitHub custom properties, before rejecting it on 2026-10-04 (DESIGN-0033 D6): repo-guardian declares its own property definitions, the built-in keys stay <code>Owner</code>/<code>Component</code>, and the only relationship permitted is fwsync generating repo-guardian policy in repo-guardian's documented format.</p>")

    x.append('<h2 id="x-ledger">Decision ledger</h2>')
    x.append("<p>Every settled choice, across the five documents. Each has a one-line rationale in its document. The later-numbered ones were added in three passes: the 2026-10-03 reconciliation after the deep dives (DESIGN-0030 D6–D7, DESIGN-0031 D7–D8, DESIGN-0032 D8–D10), the 2026-10-04 open-question answers (DESIGN-0029 D4–D5, DESIGN-0030 D8–D11, DESIGN-0031 D9–D11, DESIGN-0032 D11–D14, DESIGN-0033 D6–D7), and the 2026-10-04 application of the adversarial-review responses (DESIGN-0029 D6–D10, DESIGN-0030 D12–D18, DESIGN-0031 D12–D19, DESIGN-0032 D15–D26, DESIGN-0033 D8–D10), which also amended DESIGN-0029 D3, DESIGN-0030 D6, DESIGN-0031 D8, DESIGN-0032 D3, D6, D8, D9 and D11, and DESIGN-0033 D5 in place. On 2026-10-05 DESIGN-0029 D11, DESIGN-0030 D19 and DESIGN-0032 D27 closed INV-0021's three questions.</p>")
    for d in docs:
        if not d["decisions"]:
            continue
        x.append(f'<h4>DESIGN-{d["id"]}</h4><table><thead><tr><th style="width:60px">#</th><th style="width:34%">Decision</th><th>Rationale</th></tr></thead><tbody>')
        for num, title, rest in d["decisions"]:
            x.append(f"<tr><td>{num}</td><td>{inline(title, 'd'+d['id'])}</td><td>{inline(rest, 'd'+d['id'])}</td></tr>")
        x.append("</tbody></table>")

    x.append('<h2 id="x-oqs">Open questions</h2>')
    x.append("<p>Each document lists alternatives and an <code>other:</code> line; the recommendation is shown here. On 2026-10-04 the maintainer answered fourteen of the fifteen and deferred the last; the status column records each. CODEOWNERS ownership semantics now live in DESIGN-0034, undecided.</p>")
    for d in docs:
        if not d["oqs"]:
            continue
        x.append(f'<h4>DESIGN-{d["id"]}</h4><table><thead><tr><th style="width:60px">#</th><th style="width:30%">Question</th><th>Recommendation</th><th style="width:26%">Status</th></tr></thead><tbody>')
        for num, q, rec, res in d["oqs"]:
            hid = f"d{d['id']}-{slugify(num + ': ' + q)}"
            x.append(f'<tr><td><a href="#{hid}">{num}</a></td><td>{inline(q, "d"+d["id"])}</td><td><span class="oq-rec">{inline(rec, "d"+d["id"])}</span></td><td>{inline(res, "d"+d["id"])}</td></tr>')
        x.append("</tbody></table>")

    x.append('<h2 id="x-read">Reading order</h2><ol>')
    for did, desc in READING_ORDER:
        x.append(f'<li><a href="#d{did}">DESIGN-{did}</a> — {desc}</li>')
    x.append("</ol></section>")

    # ---- deep dives
    body = [render_reviews(docs), '<hr class="docsep">', '<section id="deep">',
            '<h1>Deep dives</h1>',
            '<p class="lede">Four cross-cutting views that put the five documents next to each other: the types and how data flows through them, the API, the database schema, and the order to build it in. Each ends with the gaps found while drawing it and the decision in the designs that resolved it. The deep dives are a 2026-10-02 snapshot and were not rewritten afterwards: their CODEOWNERS ownership examples (<code>wiz-owners</code>, rule 1.2, the effective-owner matcher) now belong to the deferred DESIGN-0034, and the custom-properties "tag schema seam" was rejected by DESIGN-0033 D6. Where a deep dive and a design body differ, the body wins.</p>',
            deep_html, '</section>']

    # ---- docs
    for d in docs:
        body.append('<hr class="docsep">')
        body.append(f'<section id="d{d["id"]}"><div class="meta">docs/design/{d["path"]}</div>')
        body.append(d["html"])
        body.append("</section>")

    page = f"""<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>repo-guardian controls redesign — DESIGN-0029 to 0034</title>
<style>{CSS}</style>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github-dark.min.css">
</head><body>
{''.join(side)}
<main>
{''.join(x)}
{''.join(body)}
</main>
<script src="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js"></script>
<script>hljs.configure({{ ignoreUnescapedHTML: true }}); hljs.highlightAll();</script>
<script type="module">
import mermaid from "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs";
mermaid.initialize({{ startOnLoad: true, theme: "dark", securityLevel: "loose", flowchart: {{ htmlLabels: true }},
  themeCSS: ".row-rect-odd path {{ fill: #121720 !important; }} .row-rect-even path {{ fill: #161b24 !important; }}",
  themeVariables: {{ background: "#0b0e13", mainBkg: "#1c2330", primaryColor: "#1c2330", primaryTextColor: "#d8dee9", primaryBorderColor: "#3a4a60",
    secondaryColor: "#15202f", secondaryBorderColor: "#2b4a6b", tertiaryColor: "#121720", tertiaryBorderColor: "#263040", lineColor: "#8b96a8", textColor: "#d8dee9",
    edgeLabelBackground: "#0b0e13", clusterBkg: "#121720", clusterBorder: "#263040", titleColor: "#d8dee9",
    noteBkgColor: "#1d1a12", noteTextColor: "#d8dee9", noteBorderColor: "#5f4a20",
    actorBkg: "#1c2330", actorBorder: "#3a4a60", actorTextColor: "#d8dee9", actorLineColor: "#3a4a60", signalColor: "#8b96a8", signalTextColor: "#d8dee9",
    labelBoxBkgColor: "#15202f", labelBoxBorderColor: "#2b4a6b", labelTextColor: "#d8dee9", loopTextColor: "#d8dee9", activationBkgColor: "#1e3350", activationBorderColor: "#6cb6ff",
    attributeBackgroundColorOdd: "#161b24", attributeBackgroundColorEven: "#121720",
    git0: "#6cb6ff", git1: "#5fd38d", git2: "#f0b84a", git3: "#c792ea", git4: "#5ccfe6", git5: "#f07178", git6: "#ff9e64", git7: "#e39ad6",
    gitBranchLabel0: "#0b0e13", gitBranchLabel1: "#0b0e13", gitBranchLabel2: "#0b0e13", gitBranchLabel3: "#0b0e13", gitBranchLabel4: "#0b0e13", gitBranchLabel5: "#0b0e13", gitBranchLabel6: "#0b0e13", gitBranchLabel7: "#0b0e13",
    cScale0: "#3a4a60", cScale1: "#2b4a6b", cScale2: "#285a3d", cScale3: "#5f4a20", cScale4: "#5f2a30", cScale5: "#4b3463", cScale6: "#1f4b55", cScale7: "#5a3b22", cScale8: "#3a4a60", cScale9: "#2b4a6b", cScale10: "#285a3d", cScale11: "#5f4a20",
    cScaleLabel0: "#d8dee9", cScaleLabel1: "#d8dee9", cScaleLabel2: "#d8dee9", cScaleLabel3: "#d8dee9", cScaleLabel4: "#d8dee9", cScaleLabel5: "#d8dee9", cScaleLabel6: "#d8dee9", cScaleLabel7: "#d8dee9", cScaleLabel8: "#d8dee9", cScaleLabel9: "#d8dee9", cScaleLabel10: "#d8dee9", cScaleLabel11: "#d8dee9",
    commitLabelColor: "#d8dee9", commitLabelBackground: "#1c2330", tagLabelColor: "#0b0e13", tagLabelBackground: "#f0b84a", tagLabelBorder: "#5f4a20",
    fontFamily: "-apple-system, BlinkMacSystemFont, Segoe UI, Inter, Roboto, sans-serif" }} }});
</script>
</body></html>
"""
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(page)
    print(f"wrote {OUT} ({len(page)} bytes)")
    for d in docs:
        print(f"  {d['id']}: {len(d['headings'])} headings, {len(d['decisions'])} decisions, {len(d['oqs'])} OQs")


if __name__ == "__main__":
    main()
