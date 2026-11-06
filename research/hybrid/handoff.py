# Cloud answers immediately; local gets ready (KV shipped, or local shadow prefill); then local takes over decode.
# Device: M4 Pro, gpt-oss-20b. Local decode ~50 tok/s at ~30k depth (63 measured at depth 0).
C_TTFT = {"cold": 2.0, "warm": 1.0}; C_TPS = 200.0; L_TPS = 50.0
READY = {  # seconds until local has a full cache for the prompt
  "KV ship, 300 Mbps q8": {"cold": 13.2, "warm": 1.7},
  "local shadow prefill": {"cold": 78.1, "warm": 10.0},
}
L_PREFILL = {"cold": 78.1, "warm": 10.0}
def cloud(n, k):  return C_TTFT[k] + n / C_TPS
def local(n, k):  return L_PREFILL[k] + n / L_TPS
def handoff(n, k, ready, catchup=0.3):
    if cloud(n, k) <= ready: return cloud(n, k), 0                      # cloud finished first: no handoff
    done = max(0.0, (ready - C_TTFT[k]) * C_TPS)                        # tokens cloud already streamed
    return ready + catchup + (n - done) / L_TPS, int(n - done)          # local finishes the rest, slower
print(f"{'turn':5s} {'output':>7s} | {'pure cloud':>10s} | {'pure local':>10s} | " +
      " | ".join(f"{r:>28s}" for r in READY))
for k in ("cold", "warm"):
    for n in (150, 1000, 3000):
        cells = []
        for r in READY:
            t, loc = handoff(n, k, READY[r][k])
            cells.append(f"{t:6.1f}s ({loc:4d} tok local)".rjust(28))
        print(f"{k:5s} {n:6d}t | {cloud(n,k):9.1f}s | {local(n,k):9.1f}s | " + " | ".join(cells))
