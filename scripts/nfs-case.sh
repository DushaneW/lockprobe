#!/usr/bin/env bash
# Mount the CI NFS export twice with OPTS and run `lockprobe pair`
# between the two mounts. Fails unless both sides exit with WANT.
set -euo pipefail
opts=$1
want=$2
a=$(mktemp -d) b=$(mktemp -d)
sudo mount -t nfs -o "$opts" 127.0.0.1:/srv/share "$a"
sudo mount -t nfs -o "$opts" 127.0.0.1:/srv/share "$b"
trap 'sudo umount "$a" "$b"' EXIT
grep -E " ($a|$b) " /proc/mounts

./lockprobe check "$a" "$b" || true

./lockprobe pair --timeout 60s "$a" >a.log 2>&1 &
pid=$!
id=""
for _ in $(seq 100); do
  id=$(grep -oE -- '--join [0-9a-f]+' a.log | cut -d' ' -f2 || true)
  [ -n "$id" ] && break
  sleep 0.1
done
[ -n "$id" ] || { cat a.log; exit 1; }

set +e
./lockprobe pair --timeout 60s --join "$id" "$b" >b.log 2>&1
cb=$?
wait $pid
ca=$?
set -e
echo "--- initiator (exit $ca)"; cat a.log
echo "--- joiner (exit $cb)"; cat b.log

# Ground truth: what real SQLite does on this mount pair.
rm -f "$a"/two-writers.db*
python3 scripts/sqlite-two-writers.py "$a/two-writers.db" "$b/two-writers.db" delete 2000 | grep -v '^err:' || true

if [ "$ca" != "$want" ] || [ "$cb" != "$want" ]; then
  echo "want exit $want on both sides, got $ca/$cb"
  exit 1
fi
