import sys, csv, os, math, statistics as st
sys.path.insert(0, sys.argv[1])
import predict as P
rows=[P.features(r) for r in csv.DictReader(open(os.path.join(sys.argv[1],"bench_data.csv")))]
main=[r for r in rows if r["fit_eligible"]=="yes" and r["metric"]=="pp"]
def report(name, rs):
    f=P.fit_pp(rs)
    errs=[]
    # LODO
    devs=sorted(set(r["device"] for r in rs))
    for d in devs:
        tr=[r for r in rs if r["device"]!=d]; te=[r for r in rs if r["device"]==d]
        ff=P.fit_pp(tr)
        if not ff: continue
        for r in te:
            p=P.pred_pp(r, ff, ff)
            errs.append(abs(p/r["meas"]-1))
    eff=[round(1/c,3) if c>0 else None for c in f[1]] if f and f[0]=="multi" else f
    print(f"{name}: n={len(rs)} eff dense/moe/attn={eff} LODO MdAPE={100*st.median(errs):.1f}% within25={100*sum(e<=.25 for e in errs)/len(errs):.0f}%")
for be in ["CUDA","Metal","ROCm","Vulkan"]:
    rs=[r for r in main if r["backend"]==be]
    report(be+" all", rs)
    rs2=[r for r in rs if "XiongjieDai" not in r["source_url"]]
    if len(rs2)!=len(rs): report(be+" excl-2024-XD", rs2)
