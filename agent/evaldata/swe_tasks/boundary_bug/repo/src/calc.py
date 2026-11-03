def clamp(v, lo, hi):
    # 基线含 bug：开区间判断导致边界值 v==lo/v==hi 时走错分支
    return v if lo < v < hi else (lo if v < lo else hi)
