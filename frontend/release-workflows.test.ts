import { execFile } from "node:child_process";
import { chmod, mkdtemp, readdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const repositoryRoot = path.resolve(import.meta.dirname, "..");
const execFileAsync = promisify(execFile);
const releaseMutationPatterns = [
  /gh release (?:create|delete|edit|publish|upload)\b/,
  /gh api(?=[^\n]*(?:releases|git\/refs\/tags))(?=[^\n]*(?:(?:--method|-X)\s+(?:DELETE|PATCH|POST|PUT)))[^\n]*/,
  /curl(?=[^\n]*(?:releases|git\/refs\/tags))(?=[^\n]*(?:(?:--request|-X)\s*(?:DELETE|PATCH|POST|PUT)))[^\n]*/,
  /github\.rest\.repos\.(?:createRelease|deleteRelease|updateRelease|uploadReleaseAsset)\b/,
  /uses:\s*(?:actions\/create-release|ncipollo\/release-action|softprops\/action-gh-release)@/,
  /git tag\b(?![^\n]*(?:--list\b|-l\b|--contains\b|--no-contains\b|--points-at\b|--merged\b|--no-merged\b|--sort\b|--column\b|--ignore-case\b|--verify\b|-v\b|-n\d*\b))[^\n]*\S/,
  /git push\b[^\n]*(?:--tags\b|--follow-tags\b|refs\/tags\/|\btag\s+\S+|\bv?\d+\.\d+\.\d+(?:[-+][^\s]+)?\b)/,
  /electron-forge publish/,
  /npm run publish/,
];

type WorkflowSource = { name: string; contents: string };

// stepRunScript returns the `run: |` body of the workflow step with this name,
// dedented, so a step's shell can be executed exactly as written.
function stepRunScript(workflow: string, stepName: string): string {
  const lines = workflow.split("\n");
  const nameAt = lines.findIndex((line) => line.trim() === `- name: ${stepName}`);
  if (nameAt < 0) throw new Error(`no step named ${stepName}`);
  const runAt = lines.findIndex((line, index) => index > nameAt && line.trim() === "run: |");
  const body: string[] = [];
  const indent = lines[runAt + 1].length - lines[runAt + 1].trimStart().length;
  for (const line of lines.slice(runAt + 1)) {
    if (line.trim() !== "" && line.length - line.trimStart().length < indent) break;
    body.push(line.slice(indent));
  }
  return body.join("\n");
}

// The one workflow allowed to create a release: the fork's tag-triggered
// draft publisher (RELEASING.md). It is held to the stricter checks in its own
// test below instead of the blanket ban. Do not add names here.
const FORK_RELEASE_WORKFLOW = "fork-release.yml";
const FORK_RELEASE_EXEMPT_PATTERNS = [
  releaseMutationPatterns[0].source,
  "write-enabled release workflow",
];

function findReleaseMutationViolations(workflows: WorkflowSource[]) {
  return workflows.flatMap(({ name, contents }) => {
    const logicalCommands = contents.replace(/\\\r?\n[ \t]*/g, " ");
    const violations = releaseMutationPatterns
      .filter((pattern) => pattern.test(logicalCommands))
      .map((pattern) => `${name}: ${pattern.source}`);

    if (
      /contents:\s*write/.test(contents) &&
      /(?:\brelease\b|git\/refs\/tags)/i.test(contents)
    ) {
      violations.push(`${name}: write-enabled release workflow`);
    }

    return violations;
  });
}

describe("desktop release workflows", () => {
  const workflowsDirectory = path.join(repositoryRoot, ".github", "workflows");
  const artifactBuilder = path.join(workflowsDirectory, "build-artifacts.yml");

  async function readWorkflows() {
    const names = (await readdir(workflowsDirectory)).filter((name) =>
      /\.ya?ml$/.test(name),
    );
    return Promise.all(
      names.map(async (name) => ({
        name,
        contents: await readFile(path.join(workflowsDirectory, name), "utf8"),
      })),
    );
  }

  it("removes obsolete public release mutation workflows", async () => {
    for (const name of [
      "frontend-release.yml",
      "feature-release-cleanup.yml",
    ]) {
      await expect(
        stat(path.join(workflowsDirectory, name)),
      ).rejects.toMatchObject({ code: "ENOENT" });
    }
  });

  it("prevents a public desktop tag publisher from returning", async () => {
    const workflows = await readWorkflows();

    expect(workflows.map(({ contents }) => contents).join("\n")).not.toContain(
      "desktop-v*",
    );
  });

  it("prevents public workflows from mutating releases or release tags", async () => {
    const workflows = await readWorkflows();

    expect(
      findReleaseMutationViolations(
        workflows.filter(({ name }) => name !== FORK_RELEASE_WORKFLOW),
      ),
    ).toEqual([]);
  });

  it("checks for an existing release or draft in the publish job, before creating one", async () => {
    const contents = await readFile(
      path.join(workflowsDirectory, FORK_RELEASE_WORKFLOW),
      "utf8",
    );
    const publishJob = contents.slice(contents.indexOf("\n  publish:\n"));
    const guard = publishJob.indexOf("- name: Refuse a tag that already has a release or draft");
    expect(guard).toBeGreaterThan(0);
    // The publish job is the only one whose token can see drafts.
    expect(publishJob.indexOf("contents: write")).toBeLessThan(guard);
    expect(guard).toBeLessThan(publishJob.indexOf("gh release create"));

    const script = stepRunScript(contents, "Refuse a tag that already has a release or draft");
    const dir = await mkdtemp(path.join(tmpdir(), "fork-release-guard-"));
    try {
      // A fake gh that answers the releases listing with FAKE_RELEASES (TSV rows,
      // as the step's --jq produces) and records how it was called.
      await writeFile(
        path.join(dir, "gh"),
        '#!/bin/sh\nprintf "%s\\n" "$*" > "$GH_ARGS"\nprintf "%b" "$FAKE_RELEASES"\n',
      );
      await chmod(path.join(dir, "gh"), 0o755);
      const guardWith = async (releases: string) => {
        try {
          const { stdout } = await execFileAsync("bash", ["-c", script], {
            env: {
              ...process.env,
              PATH: `${dir}:${process.env.PATH}`,
              GH_ARGS: path.join(dir, "args"),
              GH_REPO: "AronPerez/agent-orchestrator",
              TAG: "v0.15.3-fork.1",
              FAKE_RELEASES: releases,
            },
          });
          return { code: 0, out: stdout };
        } catch (error) {
          const failed = error as { code: number; stdout: string };
          return { code: failed.code, out: failed.stdout };
        }
      };

      expect((await guardWith("")).code).toBe(0);
      expect((await guardWith("v0.15.3-fork.10\\tpublished\\thttps://x/10\\n")).code).toBe(0);
      const draft = await guardWith("v0.15.3-fork.1\\tdraft\\thttps://x/untagged-1\\n");
      expect(draft.code).toBe(1);
      expect(draft.out).toContain("already has a release or draft");
      expect(draft.out).toContain("https://x/untagged-1");
      expect((await guardWith("v0.15.3-fork.1\\tpublished\\thttps://x/1\\n")).code).toBe(1);
      // It lists every release, drafts included, across pages.
      expect(await readFile(path.join(dir, "args"), "utf8")).toMatch(
        /^api repos\/AronPerez\/agent-orchestrator\/releases\?per_page=100 --paginate --jq /,
      );
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("lets the fork release workflow create only a gated draft release", async () => {
    const contents = await readFile(
      path.join(workflowsDirectory, FORK_RELEASE_WORKFLOW),
      "utf8",
    );

    // Only "creates a release" and "has write access" trip the blanket rule:
    // no tag pushes, no forge publish, no gh api/curl release mutations.
    expect(
      findReleaseMutationViolations([{ name: FORK_RELEASE_WORKFLOW, contents }])
        .map((violation) => violation.slice(FORK_RELEASE_WORKFLOW.length + 2))
        .sort(),
    ).toEqual([...FORK_RELEASE_EXEMPT_PATTERNS].sort());

    // Exactly one release command, and it creates a draft on the pushed tag.
    expect(contents.match(/gh release (?:create|upload|edit|delete|publish)\b/g)).toEqual([
      "gh release create",
    ]);
    expect(contents).toContain('gh release create "$TAG"');
    expect(contents).toMatch(/args=\(--draft --verify-tag /);
    expect(contents).not.toMatch(/--draft=false|make_latest|--latest\b|--prerelease=false/);

    // Read-only by default; write only in the publish job; fork repo only.
    expect(contents).toMatch(/^permissions:\s*\n\s*contents:\s*read/m);
    expect(contents.match(/contents:\s*write/g)).toHaveLength(1);
    expect(contents).toContain("if: github.repository == 'AronPerez/agent-orchestrator'");
    expect(contents).toContain(
      "if: needs.prepare.outputs.publish == 'true' && needs.prepare.outputs.sign-mac == 'true' && needs.assemble.outputs.mac-signed == 'true'",
    );

    // Releases come from fork tags on develop; a dispatch is always a dry run.
    expect(contents).toContain('- "v[0-9]+.[0-9]+.[0-9]+-fork.[0-9]+"');
    expect(contents).toContain('git merge-base --is-ancestor "$sha" origin/develop');
    expect(contents).toMatch(/tag=""\s*\n\s*version="\$INPUT_VERSION"[\s\S]*?publish=false/);

    // Never ship macOS unsigned: all six Apple secrets are required unless a
    // dry run asked to skip signing.
    for (const secret of [
      "CSC_LINK",
      "CSC_KEY_PASSWORD",
      "APPLE_SIGNING_IDENTITY",
      "APPLE_API_KEY_BASE64",
      "APPLE_API_KEY_ID",
      "APPLE_API_ISSUER",
    ]) {
      expect(contents).toContain(`HAS_${secret}: \${{ secrets.${secret} != '' }}`);
    }
  });

  it("recognizes multiline mutations without rejecting allowed workflows", () => {
    const forbidden = [
      {
        name: "multiline-gh-api.yml",
        contents: String.raw`gh api \
          --method DELETE \
          repos/o/r/releases/123`,
      },
      {
        name: "multiline-curl.yml",
        contents: String.raw`curl \
          -X POST \
          https://api.github.com/repos/o/r/releases`,
      },
      {
        name: "write-enabled-release.yml",
        contents: "name: Release publisher\npermissions:\n  contents: write\n",
      },
      {
        name: "direct-tag-publisher.yml",
        contents:
          "permissions: { contents: write }\nrun: git tag v1.2.3 && git push origin v1.2.3\n",
      },
      {
        name: "tag-ref-publisher.yml",
        contents:
          "permissions: { contents: write }\nrun: git push origin HEAD:refs/tags/v1.2.3\n",
      },
      {
        name: "annotated-tag-publisher.yml",
        contents:
          'permissions: { contents: write }\nrun: git tag -m "release" v1.2.3 && git push origin --follow-tags\n',
      },
      {
        name: "combined-annotated-tag-publisher.yml",
        contents:
          'permissions: { contents: write }\nrun: git tag -am "release" v1.2.3 && git push origin --follow-tags\n',
      },
      {
        name: "flow-write-release.yml",
        contents: "name: Release publisher\npermissions: { contents: write }\n",
      },
    ];
    const allowed = [
      {
        name: "release-monitor.yml",
        contents: String.raw`permissions:
  contents: read
run: |
  gh api \
    repos/o/r/releases/latest
  curl \
    -X GET \
    https://api.github.com/repos/o/r/releases/latest
  gh release view v1.2.3`,
      },
      {
        name: "unrelated-write.yml",
        contents:
          "name: Label issue\npermissions:\n  contents: write\nrun: gh api -X POST repos/o/r/issues/1/labels\n",
      },
      {
        name: "unrelated-git-push.yml",
        contents:
          "permissions: { contents: write }\nrun: |\n  git tag --list\n  git push origin HEAD:automation-results\n",
      },
    ];

    for (const workflow of forbidden) {
      expect(findReleaseMutationViolations([workflow])).not.toEqual([]);
    }
    expect(findReleaseMutationViolations(allowed)).toEqual([]);
  });

  it("keeps the conductor artifact builder dispatchable and read-only", async () => {
    const contents = await readFile(artifactBuilder, "utf8");

    expect(contents).toContain("workflow_dispatch:");
    expect(contents).toMatch(/permissions:\s*\n\s*contents:\s*read/);
    expect(contents).not.toContain("contents: write");
    expect(contents).not.toContain("${{ secrets.");
    expect(contents).not.toMatch(
      /(?:gh release|electron-forge publish|npm run publish)/,
    );
  });

  it("requires the WorkOS client ID for unsigned artifacts", async () => {
    const contents = await readFile(artifactBuilder, "utf8");

    expect(contents).toContain(
      "VITE_WORKOS_CLIENT_ID: ${{ vars.VITE_WORKOS_CLIENT_ID }}",
    );
    expect(contents).toContain(
      "Repository variable VITE_WORKOS_CLIENT_ID is required",
    );
  });

  it("marks artifacts as published builds and hands Sentry source maps to the conductor", async () => {
    const contents = await readFile(artifactBuilder, "utf8");

    expect(contents).toContain('AO_PUBLISHED_BUILD: "1"');
    expect(contents).toContain("name: sentry-sourcemaps-${{ matrix.platform }}");
    expect(contents).toContain("frontend/.vite/renderer/main_window/assets/*.js.map");
  });

  it("builds the Linux arm64 host package natively in every release build", async () => {
    const builder = await readFile(artifactBuilder, "utf8");
    const host = await readFile(path.join(workflowsDirectory, "linux-arm64-host.yml"), "utf8");

    expect(builder).toContain("uses: ./.github/workflows/linux-arm64-host.yml");
    expect(builder).toContain("needs: [build, host-linux-arm64]");
    expect(host).toContain("workflow_call:");
    expect(host).toContain("runs-on: ubuntu-22.04-arm");
    expect(host).toMatch(/permissions:\s*\n\s*contents:\s*read/);
    expect(host).not.toContain("${{ secrets.");
    // setup-self-hosted.sh downloads this exact asset name.
    expect(host).toContain("name: linux-arm64-host\n          path: frontend/dist-artifact/ao-host-linux-arm64.tar.gz");
    expect(host).toContain("name: digest-linux-arm64-host");
    expect(host).toContain("github.event.pull_request.number || github.run_id");
  });

  it("takes Teams for stable builds only from the VITE_AO_TEAMS repository variable", async () => {
    const contents = await readFile(artifactBuilder, "utf8");

    expect(contents).toContain("VITE_AO_TEAMS: ${{ vars.VITE_AO_TEAMS }}");
    // Never forced on in the workflow itself: stable gets Teams only when
    // someone sets the variable.
    expect(contents).not.toMatch(/VITE_AO_TEAMS:\s*["']?on\b/);
  });
});
