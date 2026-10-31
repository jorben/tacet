"""Synthetic protocol worker for subprocess tests; never a production plugin."""
import sys
from tacet_plugin_sdk.runtime import Worker


def stream(task, params):
    for index in range(params.get('fixture_count', 3)):
        task.emit({'kind': 'fixture', 'index': index})


def hang(task, params):
    """Uncooperative worker with a descendant; the supervisor must reclaim both."""
    import subprocess, time
    child = subprocess.Popen(['/bin/sleep', '300'])
    task.emit({'kind': 'fixture', 'pid': child.pid})
    while True:
        time.sleep(0.05)  # deliberately ignores task.check() and cancellation


def escape(task, params):
    """Descendant that leaves the process group (setsid), so a group kill misses it.

    The descendant is the managed interpreter rather than /bin/sleep: the kernel
    hides the environment of SIP-protected system binaries from other processes.
    """
    import subprocess
    child = subprocess.Popen([sys.executable, '-I', '-c', 'import time; time.sleep(300)'],
                             start_new_session=True)
    task.emit({'kind': 'fixture', 'pid': child.pid})


def cooperative(task, params):
    """Well-behaved long task: polls for cancellation and the deadline, emits nothing."""
    import time
    while True:
        task.check()
        time.sleep(0.01)


def oversized(task, params):
    """Emits one item larger than the byte credit granted by the host."""
    task.emit({'kind': 'fixture', 'pad': 'x' * 5000})


def explode(task, params):
    raise RuntimeError('TEST_SECRET_MUST_NOT_REACH_PROTOCOL')


if __name__ == '__main__':
    raise SystemExit(Worker(sys.stdin.buffer, sys.stdout.buffer, 'fixture.test', '0.1.0',
                           ['format'], {'fixture.stream': stream, 'fixture.explode': explode, 'fixture.hang': hang, 'fixture.escape': escape,
                            'fixture.cooperative': cooperative, 'fixture.oversized': oversized}).run())
