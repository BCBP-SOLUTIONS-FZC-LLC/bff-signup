// PR summary — posts or updates a CI status comment on the pull request.
// Called by ci.yml pr-summary job via actions/github-script.
// NEEDS_JSON env var contains the toJSON(needs) output from the workflow.

const needs = JSON.parse(process.env.NEEDS_JSON || '{}')

const status = (job) => {
  const result = needs[job]?.result
  if (result === 'success') return '✅'
  if (result === 'failure') return '❌'
  if (result === 'cancelled') return '⚠️'
  return '⏭️'
}

const rows = [
  ['validate-test',    'Tests (unit + integration + race)'],
  ['validate-quality', 'Quality (fmt · lint · vet · vuln · Helm)'],
  ['build-image',      'Docker build (cache)'],
  ['trivy',            'Trivy CVE scan + SBOM'],
  ['smoke',            'Smoke tests (size + startup gate)'],
].map(([job, label]) => `| ${status(job)} | ${label} |`).join('\n')

const body = `## CI Results

| Status | Check |
|---|---|
${rows}

> Triggered by commit \`${context.sha.slice(0, 7)}\` on \`${context.ref}\`.`

const { data: comments } = await github.rest.issues.listComments({
  owner: context.repo.owner,
  repo: context.repo.repo,
  issue_number: context.issue.number,
})

const existing = comments.find(c =>
  c.user.login === 'github-actions[bot]' && c.body.startsWith('## CI Results')
)

if (existing) {
  await github.rest.issues.updateComment({
    owner: context.repo.owner,
    repo: context.repo.repo,
    comment_id: existing.id,
    body,
  })
  core.info(`Updated existing PR summary comment #${existing.id}`)
} else {
  await github.rest.issues.createComment({
    owner: context.repo.owner,
    repo: context.repo.repo,
    issue_number: context.issue.number,
    body,
  })
  core.info('Created new PR summary comment')
}
