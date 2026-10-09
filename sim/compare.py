"""Compare v3f and v3g on random synthetic narration.

  python3 compare.py                 # amount 50, 60 seeds x 3 rooms
  python3 compare.py --amount 100
  python3 compare.py --jsfx ../ns1-clone.jsfx   # also check the JSFX file == model (needs ./jsfxrun)
"""
import argparse
import numpy as np
import ns1_model
from speech import narration, db, rms, SR

ROOMS = ((-70, .2), (-62, .2), (-66, 0))
DELAY = ns1_model.B


def segment_gains(out, x, labels, pos):
    res = {}
    for j, lab in enumerate(labels):
        a, b = pos[j], pos[j + 1]
        if a < 4 * SR or b - a < DELAY:
            continue
        if lab == 'vowel':
            a += int(.012 * SR)                  # the gate needs a few ms to open
            if b - a < int(.03 * SR):
                continue
        if lab == 'pause':                       # first 300 ms of a pause = gate release
            if b - a > int(.35 * SR):
                res.setdefault('pause>300ms', []).append(db(rms(out[a + int(.3 * SR) + DELAY:b + DELAY])) - db(rms(x[a + int(.3 * SR):b])))
            b = min(b, a + int(.3 * SR)); lab = 'pause<300ms'
        res.setdefault(lab, []).append(db(rms(out[a + DELAY:b + DELAY])) - db(rms(x[a:b])))
    return res


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--amount', type=float, default=50)
    ap.add_argument('--seeds', type=int, default=60)
    ap.add_argument('--jsfx')
    args = ap.parse_args()
    if args.jsfx:
        from jsfx_host import run_jsfx
        x, _, _ = narration(35, -62, .2)
        for fixed in (True,):
            d = np.max(np.abs(run_jsfx(args.jsfx, x, args.amount) - ns1_model.run(x, args.amount, fixed=fixed)['out']))
            print('JSFX file vs model (v3g): max difference %.2g' % d)
    stats = {False: {}, True: {}}
    for seed in range(args.seeds):
        for room_db, tilt in ROOMS:
            x, labels, pos = narration(seed, room_db, tilt)
            for fixed in (False, True):
                out = ns1_model.run(x, args.amount, fixed=fixed)['out'][:, 0]
                for k, v in segment_gains(out, x, labels, pos).items():
                    stats[fixed].setdefault(k, []).extend(v)
    keys = ['vowel', 'nasal', 'fricative', 'burst', 'breath', 'click', 'pause<300ms', 'pause>300ms']
    print('amount %g, %d recordings' % (args.amount, args.seeds * len(ROOMS)))
    for fixed in (False, True):
        v = np.array(stats[fixed]['vowel'])
        print('%s: vowels pressed below -2 dB: %d of %d, below -4 dB: %d, worst %.1f dB'
              % ('v3g' if fixed else 'v3f', (v < -2).sum(), len(v), (v < -4).sum(), v.min()))
    for title, f in (('median gain, dB', np.median), ('5th percentile (most suppressed), dB', lambda a: np.percentile(a, 5))):
        print('\n' + title)
        print('%-5s' % '' + ''.join('%12s' % k for k in keys))
        for fixed in (False, True):
            print('%-5s' % ('v3g' if fixed else 'v3f') + ''.join('%12.1f' % f(stats[fixed][k]) for k in keys))


if __name__ == '__main__':
    main()
