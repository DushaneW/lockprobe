"""Do two SQLite writers on two views of one file lose committed updates?

usage: sqlite-two-writers.py PATH_A PATH_B MODE N
PATH_A and PATH_B name the same file through (possibly) different mounts.
Only ever run against throwaway files.
"""
import multiprocessing as mp
import sqlite3
import sys
import time


def worker(path: str, mode: str, n: int, start: float, out: mp.Queue) -> None:
    con = sqlite3.connect(path, timeout=30, isolation_level=None)
    errors = 0
    while time.time() < start:
        pass
    for _ in range(n):
        try:
            con.execute("BEGIN IMMEDIATE")
            con.execute("UPDATE c SET v = v + 1")
            con.execute("COMMIT")
        except sqlite3.DatabaseError as e:
            errors += 1
            out.put(f"err:{e}")
            try:
                con.execute("ROLLBACK")
            except sqlite3.Error:
                pass
    con.close()
    out.put(f"errors:{errors}")


def main() -> None:
    a, b, mode, n = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
    con = sqlite3.connect(a, isolation_level=None)
    con.execute(f"PRAGMA journal_mode={mode}")
    con.execute("CREATE TABLE c(v INTEGER)")
    con.execute("INSERT INTO c VALUES (0)")
    con.close()
    q: mp.Queue = mp.Queue()
    start = time.time() + 0.5
    ps = [mp.Process(target=worker, args=(p, mode, n, start, q)) for p in (a, b)]
    for p in ps:
        p.start()
    for p in ps:
        p.join()
    msgs = []
    while not q.empty():
        msgs.append(q.get())
    try:
        con = sqlite3.connect(a)
        v = con.execute("SELECT v FROM c").fetchone()[0]
        ic = con.execute("PRAGMA integrity_check").fetchone()[0]
    except sqlite3.DatabaseError as e:
        v, ic = None, f"open failed: {e}"
    errs = sum(int(m.split(":")[1]) for m in msgs if m.startswith("errors:"))
    print(f"mode={mode} expected={2 * n} got={v} lost={None if v is None else 2 * n - errs - v} "
          f"txn_errors={errs} integrity={ic}")


if __name__ == "__main__":
    main()
