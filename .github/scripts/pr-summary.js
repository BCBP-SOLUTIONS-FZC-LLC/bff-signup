// Posts or updates a single PR comment summarising CI job results.
// Invoked by ci.yml via actions/github-script script-path (keeps JS out of YAML).
// NEEDS_JSON env var contains the toJSON(needs) output from the workflow.
const needs = JSON.parse(process.env.NEEDS_JSON || '{}');

function icon(r) {
  if (r === 'success')   return '✅';
  if (r === 'skipped')   return '⏭️';
  if (r === 'cancelled') return '🚫';
  return '❌';
}

const coveragePct = needs['validate-test']?.outputs?.pct;

const rows = [
  ['Tests (unit + integration + race)', 'validate-test'],
  ['Coverage ≥ 97%',                   'validate-test'],
  ['Quality (fmt · lint · vet · vuln · Helm)', 'validate-quality'],
  ['Docker build (cache)',             'build-image'],
  ['Trivy CVE scan + SBOM',           'trivy'],
  ['Smoke tests (size + startup gate)', 'smoke'],
];

const table = rows
  .map(function (row) {
    const label = row[0];
    const key   = row[1];
    const r     = needs[key]?.result || 'skipped';
    const value = (label === 'Coverage ≥ 97%' && coveragePct)
      ? parseFloat(coveragePct).toFixed(1) + '%'
      : '`' + r + '`';
    return '| ' + icon(r) + ' | ' + label + ' | ' + value + ' |';
  })
  .join('\n');

const allPassed = rows.every(function (row) {
  return needs[row[1]]?.result === 'success';
});
const headline = allPassed
  ? '✅ All checks passed — ready to merge'
  : '❌ Some checks failed';

const marker = '<!-- ci-pr-summary -->';
const body = [
  marker,
  '## ' + headline,
  '',
  '| | Check | Result |',
  '|---|---|---|',
  table,
  '',
  '📦 Image cached for `linux/amd64` · merges to `main` are signed with Cosign',
  '🔍 [Security tab](https://github.com/' +
    context.repo.owner + '/' + context.repo.repo + '/security)',
].join('\n');

const { data: comments } = await github.rest.issues.listComments({
  ...context.repo,
  issue_number: context.issue.number,
});
const existing = comments.find(function (c) {
  return c.body?.startsWith(marker);
});

if (existing) {
  await github.rest.issues.updateComment({
    ...context.repo,
    comment_id: existing.id,
    body,
  });
  core.info(`Updated existing PR summary comment #${existing.id}`);
} else {
  await github.rest.issues.createComment({
    ...context.repo,
    issue_number: context.issue.number,
    body,
  });
  core.info('Created new PR summary comment');
}
