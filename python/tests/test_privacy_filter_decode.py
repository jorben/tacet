import itertools
import math
import unittest

import numpy as np

from tacet_detect_privacy_filter.decode import (DecodeError, LabelSpace, Span, char_ranges, labels_to_spans,
                                                spans_in_text, tables, viterbi)
from tacet_detect_privacy_filter.detector import byte_decoder

NAMES = ['O'] + [f'{t}-{l}' for l in ('person', 'phone') for t in 'BIES']
SPACE = LabelSpace(NAMES)


def ids(*names):
    return [NAMES.index(n) for n in names]


def valid(path, start, end, trans):
    if not math.isfinite(start[path[0]]) or not math.isfinite(end[path[-1]]):
        return False
    return all(math.isfinite(trans[a, b]) for a, b in zip(path, path[1:]))


class TransitionTest(unittest.TestCase):
    def test_bioes_constraints(self):
        start, end, trans = tables(SPACE)
        ok = lambda a, b: math.isfinite(trans[NAMES.index(a), NAMES.index(b)])
        self.assertTrue(ok('O', 'B-person') and ok('O', 'S-phone') and ok('O', 'O'))
        self.assertFalse(ok('O', 'I-person') or ok('O', 'E-person'))
        self.assertTrue(ok('B-person', 'I-person') and ok('I-person', 'E-person') and ok('B-person', 'E-person'))
        self.assertFalse(ok('B-person', 'I-phone') or ok('B-person', 'O') or ok('I-person', 'B-person'))
        self.assertTrue(ok('E-person', 'B-phone') and ok('S-person', 'O') and ok('E-phone', 'S-person'))
        self.assertFalse(ok('E-person', 'I-person') or ok('S-phone', 'E-phone'))
        self.assertTrue(math.isfinite(start[NAMES.index('S-person')]) and not math.isfinite(start[NAMES.index('I-person')]))
        self.assertTrue(math.isfinite(end[NAMES.index('E-person')]) and not math.isfinite(end[NAMES.index('B-person')]))

    def test_biases_land_on_their_edges(self):
        _, _, trans = tables(SPACE, {'transition_bias_background_stay': -1.0, 'transition_bias_inside_to_end': 2.0,
                                     'transition_bias_end_to_start': 3.0})
        self.assertEqual(trans[NAMES.index('O'), NAMES.index('O')], -1.0)
        self.assertEqual(trans[NAMES.index('I-person'), NAMES.index('E-person')], 2.0)
        self.assertEqual(trans[NAMES.index('E-person'), NAMES.index('B-phone')], 3.0)
        self.assertEqual(trans[NAMES.index('B-person'), NAMES.index('I-person')], 0.0)

    def test_label_space_needs_background_and_all_four_tags(self):
        for names in (['B-x', 'I-x', 'E-x', 'S-x'], ['O', 'B-x', 'I-x', 'E-x'], ['O', 'Q-x']):
            with self.subTest(names=names), self.assertRaises(DecodeError):
                LabelSpace(names)


class ViterbiTest(unittest.TestCase):
    def test_matches_brute_force_over_valid_paths(self):
        start, end, trans = tables(SPACE, {'transition_bias_background_to_start': -0.3, 'transition_bias_inside_to_continue': 0.2})
        rng = np.random.default_rng(7)
        for _ in range(25):
            t = int(rng.integers(1, 5))
            lp = np.log(rng.dirichlet(np.ones(len(NAMES)), size=t))
            best, best_score = None, -math.inf
            for path in itertools.product(range(len(NAMES)), repeat=t):
                if not valid(path, start, end, trans):
                    continue
                s = start[path[0]] + end[path[-1]] + sum(lp[i, k] for i, k in enumerate(path))
                s += sum(trans[a, b] for a, b in zip(path, path[1:]))
                if s > best_score:
                    best, best_score = list(path), s
            self.assertEqual(viterbi(lp, start, end, trans), best)

    def test_argmax_that_breaks_bioes_is_repaired(self):
        start, end, trans = tables(SPACE)
        lp = np.log(np.full((3, len(NAMES)), 1e-6))
        for i, n in enumerate(('I-person', 'I-person', 'O')):  # argmax starts inside a span
            lp[i, NAMES.index(n)] = math.log(0.6)
        lp[0, NAMES.index('B-person')] = math.log(0.3)
        lp[1, NAMES.index('E-person')] = math.log(0.3)
        self.assertEqual(viterbi(lp, start, end, trans), ids('B-person', 'E-person', 'O'))

    def test_bad_output_fails_closed(self):
        start, end, trans = tables(SPACE)
        for lp in (np.zeros((2, 3)), np.full((2, len(NAMES)), np.nan)):
            with self.subTest(shape=lp.shape), self.assertRaises(DecodeError):
                viterbi(lp, start, end, trans)


class SpansTest(unittest.TestCase):
    def test_labels_to_spans_like_the_reference(self):
        path = ids('O', 'B-person', 'I-person', 'E-person', 'S-phone', 'O', 'I-phone', 'E-phone', 'E-person', 'B-person')
        self.assertEqual(labels_to_spans(path, SPACE),
                         [('person', 1, 4), ('phone', 4, 5), ('phone', 6, 8), ('person', 8, 9), ('person', 9, 10)])

    def test_char_ranges_from_token_bytes_cover_split_characters(self):
        text = '张伟 a'
        raw = text.encode('utf-8')  # 张 = 3 bytes, 伟 = 3 bytes
        starts, ends = char_ranges([raw[:2], raw[2:4], raw[4:6], raw[6:]], text)
        self.assertEqual(list(zip(starts, ends)), [(0, 1), (0, 2), (1, 2), (2, 4)])
        with self.assertRaises(DecodeError):
            char_ranges([b'x'], text)

    def test_trim_overlap_and_score(self):
        text = ' 张伟 13800000000'
        lp = np.log(np.full((4, len(NAMES)), 0.01))
        path = ids('B-person', 'E-person', 'S-phone', 'O')
        for i, (k, p) in enumerate(zip(path, (0.9, 0.7, 0.95, 0.8))):
            lp[i, k] = math.log(p)
        starts, ends = [0, 1, 3, 15], [2, 3, 15, 15]
        got = spans_in_text(path, lp, SPACE, starts, ends, text, floor=0.0)
        self.assertEqual([(s.label, text[s.start:s.end]) for s in got], [('person', '张伟'), ('phone', '13800000000')])
        self.assertAlmostEqual(got[0].score, 0.7)
        self.assertEqual(spans_in_text(path, lp, SPACE, starts, ends, text, floor=0.8),
                         [Span('phone', 4, 15, got[1].score)])

    def test_byte_level_alphabet_is_a_bijection(self):
        dec = byte_decoder()
        self.assertEqual(sorted(dec.values()), list(range(256)))
        self.assertEqual(dec['Ġ'], 0x20)
        self.assertEqual(dec['a'], ord('a'))


if __name__ == '__main__':
    unittest.main()
