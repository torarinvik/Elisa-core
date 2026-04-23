#!/usr/bin/env python3
"""Run a command with a hard timeout and kill its whole process group on expiry."""

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys
from typing import Sequence


DEFAULT_GRACE_SECONDS = 2.0
TIMEOUT_EXIT_CODE = 124


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Run a command with a timeout. On expiry the wrapper sends SIGTERM to "
            "the child's process group, waits briefly, then sends SIGKILL if needed."
        )
    )
    parser.add_argument(
        "--cwd",
        default=None,
        help="Working directory for the command (defaults to current directory)",
    )
    parser.add_argument(
        "--grace-seconds",
        type=float,
        default=DEFAULT_GRACE_SECONDS,
        help="How long to wait after SIGTERM before SIGKILL (default: 2)",
    )
    return parser


def parse_cli(argv: Sequence[str]) -> tuple[float, argparse.Namespace, list[str]]:
    if len(argv) < 2:
        raise SystemExit("run_with_timeout.py: missing timeout seconds")

    try:
        seconds = float(argv[1])
    except ValueError as exc:
        raise SystemExit(f"run_with_timeout.py: invalid timeout seconds: {argv[1]}") from exc

    remainder = list(argv[2:])
    command: list[str]
    option_argv: list[str]
    if "--" in remainder:
        split_index = remainder.index("--")
        option_argv = remainder[:split_index]
        command = remainder[split_index + 1 :]
    else:
        option_argv = []
        command = remainder

    parser = build_parser()
    args = parser.parse_args(option_argv)
    if not command:
        raise SystemExit("run_with_timeout.py: missing command after --")
    return seconds, args, command


def terminate_process_group(process: subprocess.Popen[object], grace_seconds: float) -> None:
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return

    try:
        process.wait(timeout=grace_seconds)
        return
    except subprocess.TimeoutExpired:
        pass

    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    process.wait()


def main() -> int:
    seconds, args, command = parse_cli(sys.argv)
    parser = build_parser()

    if seconds <= 0:
        parser.error("seconds must be greater than 0")
    if args.grace_seconds < 0:
        parser.error("--grace-seconds must be non-negative")

    try:
        process = subprocess.Popen(command, cwd=args.cwd, start_new_session=True)
    except FileNotFoundError:
        print(f"run_with_timeout.py: command not found: {command[0]}", file=sys.stderr)
        return 127

    try:
        return_code = process.wait(timeout=seconds)
    except subprocess.TimeoutExpired:
        print(
            (
                f"timeout: exceeded {seconds:g}s; terminating process group "
                f"for pid {process.pid}"
            ),
            file=sys.stderr,
        )
        terminate_process_group(process, args.grace_seconds)
        return TIMEOUT_EXIT_CODE
    except KeyboardInterrupt:
        print("interrupted: forwarding SIGINT to child process group", file=sys.stderr)
        try:
            os.killpg(process.pid, signal.SIGINT)
        except ProcessLookupError:
            pass
        return process.wait()

    if return_code >= 0:
        return return_code
    return 128 + abs(return_code)


if __name__ == "__main__":
    raise SystemExit(main())