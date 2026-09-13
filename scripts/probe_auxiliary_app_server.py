"""Bounded, isolated native app-server startup check. Never starts an inference."""
import argparse
import json
import os
import queue
import subprocess
import tempfile
import threading
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", required=True)
    parser.add_argument("--real-executable", required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="router-auxiliary-probe-") as root:
        env = {k: v for k, v in os.environ.items() if not k.upper().startswith(("CODEX_", "OPENAI_"))}
        env.update(CODEX_HOME=root, CODEX_SQLITE_HOME=root, CODEX_MUX_REAL_CODEX=args.real_executable)
        process = subprocess.Popen([args.executable, "app-server"], cwd=root, env=env,
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, encoding="utf-8", creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
        responses = queue.Queue()

        def consume():
            for line in process.stdout:
                try:
                    responses.put(json.loads(line))
                except ValueError:
                    pass
            responses.put(None)

        reader = threading.Thread(target=consume, daemon=True)
        reader.start()
        # Drain diagnostics to avoid blocking; do not expose unrelated config or paths.
        def drain():
            for _ in process.stderr:
                pass
        diagnostics = threading.Thread(target=drain, daemon=True)
        diagnostics.start()
        try:
            message = {"id": 1, "method": "initialize", "params": {"clientInfo": {"name": "auxiliary_probe", "version": "1"}}}
            process.stdin.write(json.dumps(message) + "\n")
            process.stdin.flush()
            while True:
                reply = responses.get(timeout=20)
                if reply is None:
                    raise RuntimeError("Auxiliary process exited before initialize response")
                if reply.get("id") == 1:
                    if "error" in reply or "result" not in reply:
                        raise RuntimeError("Auxiliary initialization rejected")
                    print("PASS: auxiliary app-server initialize answered; no inference requested.")
                    break
        finally:
            # EOF allows the wrapper to reap its own original app-server child.
            process.stdin.close()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
                raise RuntimeError("Auxiliary app-server did not stop on EOF")
            reader.join(timeout=2)
            diagnostics.join(timeout=2)
            process.stdout.close()
            process.stderr.close()
            # Windows can release the exited native process's cwd asynchronously.
            time.sleep(1)


if __name__ == "__main__":
    main()
