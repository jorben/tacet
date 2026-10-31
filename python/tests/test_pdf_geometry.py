import json
import math
import pathlib
import unittest

from tacet_format_pdf.geometry import GeometryError, regions

VECTORS = pathlib.Path(__file__).resolve().parents[2] / 'protocol' / 'testdata' / 'regions-v1.json'


class GeometryTest(unittest.TestCase):
    def test_every_shared_vector_gives_the_rectangles_the_go_implementation_gives(self):
        cases = json.loads(VECTORS.read_text(encoding='utf-8'))['cases']
        self.assertGreaterEqual(len(cases), 8)
        for case in cases:
            with self.subTest(case['name']):
                got = regions([tuple(b) for b in case['boxes'] or []], case['page_w'], case['page_h'], case['dpi'],
                              case['margin_points'], case['antialias_pixels'])
                self.assertEqual([list(r) for r in got], case['rects'])

    def test_invalid_input_is_refused_not_guessed(self):
        ok = [(1, 1, 5, 5)]
        for name, args in {'nan box': ([(math.nan, 1, 5, 5)], 100, 100, 72), 'inverted x': ([(5, 1, 1, 5)], 100, 100, 72),
                           'flat box': ([(1, 5, 5, 5)], 100, 100, 72), 'no page': (ok, 0, 100, 72), 'no dpi': (ok, 100, 100, 0),
                           'infinite page': (ok, math.inf, 100, 72)}.items():
            with self.subTest(name), self.assertRaises(GeometryError):
                regions(*args)
        with self.assertRaises(GeometryError):
            regions(ok, 100, 100, 72, margin=-1)
        with self.assertRaises(GeometryError):
            regions(ok, 100, 100, 72, antialias=17)


if __name__ == '__main__':
    unittest.main()
