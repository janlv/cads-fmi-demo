import os, time, json
try:
    import yaml
except ImportError as exc:
    raise RuntimeError("PyYAML is required to load producer configuration") from exc
from fmpy import simulate_fmu

def build_paths():
    base = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    fmu_root = os.path.join(base, "fmu")
    artifacts_root = os.path.join(fmu_root, "artifacts")
    build_dir = os.path.join(artifacts_root, "build")
    config_dir = os.path.join(base, "config")
    os.makedirs(build_dir, exist_ok=True)
    os.makedirs(config_dir, exist_ok=True)
    return {
        "base": base,
        "fmu_root": fmu_root,
        "artifacts": artifacts_root,
        "build": build_dir,
        "producer_fmu": os.path.join(build_dir, "Producer.fmu"),
        "consumer_fmu": os.path.join(build_dir, "Consumer.fmu"),
        "out_json": os.path.join(base, "data", "producer_result.json"),
        "producer_config": os.path.join(config_dir, "producer.yaml")
    }

def load_yaml_config(path):
    if not os.path.exists(path):
        return {}
    with open(path, "r") as f:
        data = yaml.safe_load(f) or {}
    if not isinstance(data, dict):
        raise ValueError(f"Producer config at {path} must be a mapping")
    return data

def main():
    p = build_paths()

    # 1) Run Producer
    print("==> Running Producer FMU ...", flush=True)
    producer_cfg = load_yaml_config(p["producer_config"])
    raw_points = producer_cfg.get("num_points", 10000)
    try:
        producer_num_points = max(1, int(raw_points))
    except (TypeError, ValueError):
        producer_num_points = 10000
    start_values = {'num_points': producer_num_points}
    res = simulate_fmu(
        filename=p["producer_fmu"],
        start_values=start_values,
        output=['mean', 'std', 'vmin', 'vmax', 'rollingMean', 'done']
    )

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
    res2 = simulate_fmu(
        filename=p["consumer_fmu"],
        start_values=start_values,
        output=['health_score', 'anomaly']
    )

    last2 = res2[-1]
    summary = {"health_score": float(last2['health_score']),
               "anomaly": bool(last2['anomaly'])}
    print("Consumer summary:", summary, flush=True)

    with open(os.path.join(p["base"], "data", "consumer_result.json"), "w") as f:
        json.dump(summary, f, indent=2)

if __name__ == "__main__":
    main()
