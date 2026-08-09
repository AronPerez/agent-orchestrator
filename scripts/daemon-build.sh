#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
backend_dir="${repo_root}/backend"
build_dir="${XDG_CACHE_HOME:-${HOME}/.cache}/aoagents/agent-orchestrator/bin"

can_write_dir() {
  local dir="$1"

  [[ -n "${dir}" ]] || return 1
  mkdir -p "${dir}"
  [[ -d "${dir}" && -w "${dir}" ]]
}

resolve_ao() {
  local resolved

  resolved="$(command -v ao || true)"
  if [[ -z "${resolved}" && -n "${goexe:-}" ]]; then
    resolved="$(command -v "ao${goexe}" || true)"
  fi

  printf '%s\n' "${resolved}"
}

absolute_path() {
  local path="$1"

  printf '%s/%s\n' "$(cd "$(dirname "${path}")" && pwd -P)" "$(basename "${path}")"
}

install_file() {
  local source_path="$1"
  local target_path="$2"

  if ln -sfn "${source_path}" "${target_path}" 2>/dev/null; then
    printf 'Linked %s\n' "${target_path}"
  else
    rm -f "${target_path}"
    cp "${source_path}" "${target_path}"
    chmod +x "${target_path}"
    printf 'Installed %s\n' "${target_path}"
  fi
}

select_install_dir() {
  local gopath
  local existing_path
  local dir
  local candidate
  local -a path_entries
  gopath="$(go env GOPATH)"
  existing_path="$(resolve_ao)"

  if [[ -n "${existing_path}" && "${existing_path}" = /* ]] && can_write_dir "$(dirname "${existing_path}")"; then
    dirname "${existing_path}"
    return 0
  fi

  local candidates=(
    "${gopath}/bin"
    "/usr/local/bin"
    "/opt/homebrew/bin"
    "${HOME}/.local/bin"
  )

  IFS=':' read -r -a path_entries <<< "${PATH:-}"
  for dir in "${path_entries[@]}"; do
    for candidate in "${candidates[@]}"; do
      if [[ "${dir}" == "${candidate}" ]] && can_write_dir "${dir}"; then
        printf '%s\n' "${dir}"
        return 0
      fi
    done
  done

  for dir in "${path_entries[@]}"; do
    if [[ "${dir}" = /* ]] && can_write_dir "${dir}"; then
      printf '%s\n' "${dir}"
      return 0
    fi
  done

  return 1
}

command -v go >/dev/null
goexe="$(go env GOEXE)"
binary_name="ao${goexe}"
binary_path="${build_dir}/${binary_name}"

mkdir -p "${build_dir}"

# Regenerate the browser bundle the daemon embeds and serves at its own origin,
# so an `ao` installed from source has the web UI too. Skipped when frontend deps
# are absent — go:embed still resolves against the tracked .gitkeep, and the
# daemon then answers UI requests with a 503 that says the bundle was not built.
frontend_dir="${repo_root}/frontend"
webui_bundle="${backend_dir}/internal/httpd/webui/bundle"
if [[ -x "${frontend_dir}/node_modules/.bin/vite" ]]; then
  rm -rf "${webui_bundle}"
  (cd "${frontend_dir}" && VITE_AO_WEB=1 ./node_modules/.bin/vite build \
    --config vite.renderer.config.ts --outDir "${webui_bundle}" --emptyOutDir)
  mkdir -p "${webui_bundle}" && : > "${webui_bundle}/.gitkeep"
  # go:embed is satisfied by .gitkeep alone, so an empty bundle would still
  # compile and install a daemon whose UI answers 503. Fail here instead.
  if [[ ! -f "${webui_bundle}/index.html" ]]; then
    printf 'Web UI bundle missing: %s/index.html does not exist after the vite build.\n' "${webui_bundle}" >&2
    printf 'The daemon would install with no UI (503 on every page request).\n' >&2
    exit 1
  fi
else
  # Name the consequence, not just the skip: "Skipping" alone reads as an
  # optimisation, and the resulting daemon looks fine until someone opens it in a
  # browser. Mirrors the wording of the 503 the daemon itself will serve.
  printf 'Skipping the web UI bundle: %s/node_modules is missing.\n' "${frontend_dir}" >&2
  printf '  This daemon will answer every web UI request with 503 "web UI bundle was not built into this daemon".\n' >&2
  printf '  Run (cd %s && npm install) and rebuild if you want the browser UI.\n' "${frontend_dir}" >&2
fi

(cd "${backend_dir}" && go build -o "${binary_path}" ./cmd/ao)

if ! install_dir="$(select_install_dir)"; then
  printf 'Could not find a writable directory on PATH for ao\n' >&2
  exit 1
fi
install_path="${install_dir}/${binary_name}"
shim_path=""

install_file "${binary_path}" "${install_path}"

if [[ -n "${goexe}" ]]; then
  shim_path="${install_dir}/ao"
  printf '%s\n' \
    '#!/usr/bin/env bash' \
    'script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"' \
    'exec "${script_dir}/ao.exe" "$@"' > "${shim_path}"
  chmod +x "${shim_path}"
fi

resolved="$(resolve_ao)"
if [[ -z "${resolved}" ]]; then
  printf 'ao did not resolve on PATH after installing %s\n' "${install_path}" >&2
  exit 1
fi
resolved_path="$(absolute_path "${resolved}")"
install_abs_path="$(absolute_path "${install_path}")"
shim_abs_path=""
if [[ -n "${shim_path}" ]]; then
  shim_abs_path="$(absolute_path "${shim_path}")"
fi
if [[ "${resolved_path}" != "${install_abs_path}" && "${resolved_path}" != "${shim_abs_path}" ]]; then
  printf 'ao resolves to %s, expected %s\n' "${resolved}" "${install_path}" >&2
  exit 1
fi

printf 'Built %s\n' "${binary_path}"
