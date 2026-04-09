# scripts/dev.ps1 — helper for common docker compose tasks (PowerShell / Windows).
# Run from the repository root:  .\scripts\dev.ps1 <command>
# Supported commands: up | down | logs | doctor

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('up', 'down', 'logs', 'doctor', 'help')]
    [string]$Command = 'help',

    # Optional service name for the 'logs' command
    [Parameter(Position = 1)]
    [string]$Service = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ComposeFile = 'docker-compose.yml'

# ── guard: must be run from the repo root ────────────────────────────────────
if (-not (Test-Path $ComposeFile)) {
    Write-Host ""
    Write-Host "ERROR: $ComposeFile not found in the current directory." -ForegroundColor Red
    Write-Host ""
    Write-Host "  You must run this script from the repository root, e.g.:"
    Write-Host "    cd C:\path\to\AIISTECH-Backend"
    Write-Host "    .\scripts\dev.ps1 up"
    Write-Host ""
    Write-Host "  Or pass the compose file explicitly:"
    Write-Host "    docker compose -f C:\path\to\AIISTECH-Backend\$ComposeFile up -d --build"
    Write-Host ""
    exit 1
}

switch ($Command) {
    'up' {
        Write-Host "▶  Starting services (detached, with build)…"
        docker compose up -d --build
        Write-Host "✔  Services started. Logs: .\scripts\dev.ps1 logs" -ForegroundColor Green
    }
    'down' {
        Write-Host "▶  Stopping services…"
        docker compose down
        Write-Host "✔  Services stopped." -ForegroundColor Green
    }
    'logs' {
        if ($Service -ne '') {
            docker compose logs -f $Service
        } else {
            docker compose logs -f
        }
    }
    'doctor' {
        Write-Host "─── dev doctor ─────────────────────────────────────────────"
        Write-Host ""

        $failed = $false

        # Docker daemon reachable?
        $dockerInfo = docker info 2>&1
        if ($LASTEXITCODE -eq 0) {
            Write-Host "  ✔  Docker daemon is reachable." -ForegroundColor Green
        } else {
            Write-Host "  ✘  Docker daemon is NOT reachable." -ForegroundColor Red
            Write-Host "     • Open Docker Desktop and wait for 'Engine running'."
            Write-Host "     • Then verify: docker info"
            Write-Host "     See docs\windows-docker-setup.md for full troubleshooting."
            $failed = $true
        }

        # docker-compose.yml present?
        if (Test-Path $ComposeFile) {
            Write-Host "  ✔  $ComposeFile found." -ForegroundColor Green
        } else {
            Write-Host "  ✘  $ComposeFile not found — run from repo root." -ForegroundColor Red
            $failed = $true
        }

        # .env present?
        if (Test-Path '.env') {
            Write-Host "  ✔  .env file found." -ForegroundColor Green
        } else {
            Write-Host "  ⚠  .env file missing — copy .env.example:" -ForegroundColor Yellow
            Write-Host "       Copy-Item .env.example .env"
        }

        Write-Host ""
        if ($failed) {
            Write-Host "  ✘  One or more checks failed. Fix the issues above and re-run." -ForegroundColor Red
            exit 1
        } else {
            Write-Host "  ✔  All checks passed. Run:  .\scripts\dev.ps1 up" -ForegroundColor Green
        }
        Write-Host "────────────────────────────────────────────────────────────"
    }
    default {
        Write-Host "Usage: .\scripts\dev.ps1 <command>"
        Write-Host ""
        Write-Host "Commands:"
        Write-Host "  up       Build images and start services in the background."
        Write-Host "  down     Stop and remove containers."
        Write-Host "  logs [service]  Follow container logs (optionally pass a service name)."
        Write-Host "  doctor   Check Docker daemon, compose file, and .env prerequisites."
        Write-Host ""
        Write-Host "Must be run from the repository root (where $ComposeFile lives)."
    }
}
