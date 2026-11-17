"""Block until the three lane reports exist or a lane agent looks stalled.

Prints exactly one line and exits:
  ALL_DONE                         every lane report exists
  STALLED <agent> <package> <st>   agent not working for 3 polls and no report
  STILL_WAITING <packages>         nothing happened within the window; run again
"""

import pathlib
import re
import subprocess
import sys
import time

TASK_DIR = pathlib.Path(__file__).resolve().parent.parent
LANES = {"na-pi-1": "PI-1", "na-pi-2": "PI-2", "na-pi-3": "PI-3"}
POLL_SECONDS = 60
WINDOW_SECONDS = 10 * 60
STALL_POLLS = 3

not_working = dict.fromkeys(LANES, 0)
started = time.time()

while True:
    pending = [
        agent
        for agent, package in LANES.items()
        if not (TASK_DIR / "reports" / f"{package}.md").exists()
    ]
    if not pending:
        print("ALL_DONE")
        sys.exit(0)

    for agent in pending:
        result = subprocess.run(
            ["herdr", "agent", "get", agent],
            capture_output=True,
            encoding="utf-8",
            errors="replace",
        )
        match = re.search(r'"agent_status":"(\w+)"', result.stdout or "")
        status = match.group(1) if match else "missing"
        not_working[agent] = 0 if status == "working" else not_working[agent] + 1
        if not_working[agent] >= STALL_POLLS:
            print(f"STALLED {agent} {LANES[agent]} {status}")
            sys.exit(0)

    if time.time() - started > WINDOW_SECONDS:
        print("STILL_WAITING " + ",".join(LANES[agent] for agent in pending))
        sys.exit(0)

    time.sleep(POLL_SECONDS)
