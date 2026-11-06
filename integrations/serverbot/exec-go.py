"""Compatibility entrypoint for the unchanged systemd unit; immediately execs Go."""
import os
from pathlib import Path
root = Path(__file__).resolve().parent
binary = root / "serverbot-go"
os.execv(str(binary), [str(binary), "--root", str(root)])
