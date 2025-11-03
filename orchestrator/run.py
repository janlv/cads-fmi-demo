import os, time, json
from fmpy import simulate_fmu

def build_paths():
    base = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    dist = os.path.join(base, "dist")
    os.makedirs(dist, exist_ok=True)
    return {
        "base": base,
        "dist": dist,
        "producer_fmu": os.path.join(dist, "Producer.fmu"),
        "consumer_fmu": os.path.join(dist, "Consumer.fmu"),
        "out_json": os.path.join(base, "data", "producer_result.json")
    }

def main():
    p = build_paths()

    # 1) Run Producer
    print("==> Running Producer FMU ...", flush=True)
    res = simulate_fmu(filename=p["producer_fmu"],
                       start_time=0.0, stop_time=30.0, step_size=0.1,
                       output=['mean','std','vmin','vmax','rollingMean','done'])

    # take last row
    last = res[-1]  # numpy structured array row
    out = {
        "mean": float(last['mean']),
        "std": float(last['std']),
        "vmin": float(last['vmin']),
        "vmax": float(last['vmax']),
        "rollingMean": float(last['rollingMean'])
    }

    os.makedirs(os.path.join(p["base"], "data"), exist_ok=True)
    with open(p["out_json"], "w") as f:
        json.dump(out, f, indent=2)
    print("Producer outputs:", out, flush=True)

    # 2) Run Consumer using start_values
    print("==> Running Consumer FMU ...", flush=True)
    start_values = {
        'mean_in': out["mean"],
        'std_in': out["std"],
        'min_in': out["vmin"],
        'max_in': out["vmax"],
        'rm_in': out["rollingMean"]
    }
    res2 = simulate_fmu(filename=p["consumer_fmu"],
                        start_time=0.0, stop_time=1.0, step_size=0.1,
                        start_values=start_values,
                        output=['health_score','anomaly'])

    last2 = res2[-1]
    summary = {"health_score": float(last2['health_score']),
               "anomaly": bool(last2['anomaly'])}
    print("Consumer summary:", summary, flush=True)

    with open(os.path.join(p["base"], "data", "consumer_result.json"), "w") as f:
        json.dump(summary, f, indent=2)

if __name__ == "__main__":
    main()
