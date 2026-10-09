"""Run a .jsfx file outside REAPER through ysfx (see build_host.sh)."""
import os, subprocess, tempfile
import numpy as np

HOST = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'jsfxrun')


def run_jsfx(path, x, amount=50, srate=48000):
    if x.ndim == 1:
        x = np.stack([x, x], 1)
    with tempfile.TemporaryDirectory() as d:
        i, o = os.path.join(d, 'in.f64'), os.path.join(d, 'out.f64')
        np.ascontiguousarray(x, dtype='<f8').tofile(i)
        subprocess.run([HOST, os.path.abspath(path), str(srate), str(amount), i, o], check=True)
        return np.fromfile(o, dtype='<f8').reshape(-1, 2)
