"""Real protocol entry for the XLSX plugin."""
import sys

from tacet_plugin_sdk.runtime import Worker
from tacet_plugin_sdk.unitkit import handlers

from .adapter import open_adapter

if __name__ == '__main__':
    raise SystemExit(Worker(sys.stdin.buffer, sys.stdout.buffer, 'tacet.xlsx', '0.1.0', ['format'], handlers(open_adapter)).run())
