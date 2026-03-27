#!/usr/bin/env bash
# scripts/dev.sh — helper for common docker compose tasks.
# Run from the repository root:  bash scripts/dev.sh <command>
# Supported commands: up | down | logs | doctor

set -euo pipefail

COMPOSE_FILE="docker-compose.yml"

# ── guard: must be run from the repo root ────────────────────────────────────
if [[ ! -f "$COMPOSE_FILE" ]]; then
  echo ""
  echo "ERROR: $COMPOSE_FILE not found in the current directory."
  echo ""
  echo "  You must run this script from the repository root, e.g.:"
  echo "    cd /path/to/AIISTECH-Backend"
  echo "    bash scripts/dev.sh up"
  echo ""
  echo "  Or pass the compose file explicitly:"
  echo "    docker compose -f /path/to/AIISTECH-Backend/$COMPOSE_FILE up -d --build"
  echo ""
  exit 1
fi

CMD="${1:-help}"

case "$CMD" in
  up)
    echo "▶  Starting services (detached, with build)…"
    docker compose up -d --build
    echo "✔  Services started. Logs: bash scripts/dev.sh logs"
    ;;
  down)
    echo "▶  Stopping services…"
    docker compose down
    echo "✔  Services stopped."
    ;;
  logs)
    docker compose logs -f "${2:-}"
    ;;
  doctor)
    echo "─── dev doctor ────────────────────────────────────────────"
    echo ""

    # Docker daemon reachable?
    if docker info > /dev/null 2>&1; then
      echo "  ✔  Docker daemon is reachable."
    else
      echo "  ✘  Docker daemon is NOT reachable."
      echo "     • On Windows: open Docker Desktop and wait for 'Engine running'."
      echo "     • Then verify: docker info"
      echo "     See docs/windows-docker-setup.md for full troubleshooting."
      DOCTOR_FAILED=1
    fi

    # docker-compose.yml present?
    if [[ -f "$COMPOSE_FILE" ]]; then
      echo "  ✔  $COMPOSE_FILE found."
    else
      echo "  ✘  $COMPOSE_FILE not found — run from repo root."
      DOCTOR_FAILED=1
    fi

    # .env present?
    if [[ -f ".env" ]]; then
      echo "  ✔  .env file found."
    else
      echo "  ⚠  .env file missing — copy .env.example:"
      echo "       cp .env.example .env"
    fi

    echo ""
    if [[ "${DOCTOR_FAILED:-0}" == "1" ]]; then
      echo "  ✘  One or more checks failed. Fix the issues above and re-run."
      exit 1
    else
      echo "  ✔  All checks passed. Run:  bash scripts/dev.sh up"
    fi
    echo "───────────────────────────────────────────────────────────"
    ;;
  help|--help|-h|*)
    echo "Usage: bash scripts/dev.sh <command>"
    echo ""
    echo "Commands:"
    echo "  up       Build images and start services in the background."
    echo "  down     Stop and remove containers."
    echo "  logs [service]  Follow container logs (optionally pass a service name)."
    echo "  doctor   Check Docker daemon, compose file, and .env prerequisites."
    echo ""
    echo "Must be run from the repository root (where $COMPOSE_FILE lives)."
    ;;
esac
