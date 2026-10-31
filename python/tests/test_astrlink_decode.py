import json
import unittest
from pathlib import Path

from tacet_detect_astrlink.decode import DecodeError, LABELS, Span, decode_window, merge

ROOT = Path(__file__).resolve().parents[2]
MODEL_DIR = ROOT / '.pi/m0-resources/astrlink-guard-int8'


def row(label, strength=8.0):
    values = [0.0] * len(LABELS)
    values[LABELS.index(label)] = strength
    return values


class DecodeTest(unittest.TestCase):
    def test_bio_spans_and_special_tokens(self):
        offsets = [(0, 0), (0, 1), (1, 2), (2, 3), (3, 4), (0, 0)]
        logits = [row('O'), row('B-private_person'), row('I-private_person'), row('O'),
                  row('B-phone'), row('O')]
        spans = decode_window(4, offsets, logits)
        self.assertEqual([(s.label, s.start, s.end) for s in spans],
                         [('private_person', 0, 2), ('phone', 3, 4)])

    def test_inside_without_begin_or_label_change_starts_new_span(self):
        offsets = [(0, 1), (1, 2), (2, 3)]
        logits = [row('I-email'), row('I-email'), row('I-url')]
        self.assertEqual([(s.label, s.start, s.end) for s in decode_window(3, offsets, logits)],
                         [('email', 0, 2), ('url', 2, 3)])

    def test_adjacent_tokens_with_overlapping_offsets_stay_one_span(self):
        # SentencePiece offsets can include the preceding space, overlapping by one character
        offsets = [(0, 4), (4, 6), (5, 7), (7, 9), (8, 10)]
        logits = [row('B-private_date'), row('I-private_date'), row('I-private_date'),
                  row('I-private_date'), row('I-private_date')]
        self.assertEqual([(s.label, s.start, s.end) for s in decode_window(10, offsets, logits)],
                         [('private_date', 0, 10)])

    def test_floor_and_min_span_score(self):
        low = row('B-account', 0.1)
        self.assertEqual(decode_window(1, [(0, 1)], [low], floor=0.9), [])
        self.assertEqual(len(decode_window(1, [(0, 1)], [low], floor=0.0)), 1)

    def test_invalid_model_output_fails_closed(self):
        for offsets, logits in (([(0, 3)], [row('O')]), ([(0, 1)], [[0.0] * 3]),
                                ([(0, 1)], [[float('nan')] * len(LABELS)]),
                                ([(0, 1), (1, 2)], [row('O')])):
            with self.subTest(offsets=offsets), self.assertRaises(DecodeError):
                decode_window(2, offsets, logits)

    def test_merge_overlapping_windows_keeps_label_and_best_score(self):
        merged = merge([Span('phone', 5, 10, 0.7), Span('phone', 8, 14, 0.95), Span('email', 8, 12, 0.9),
                        Span('phone', 20, 22, 0.99)])
        self.assertEqual(merged, [Span('phone', 5, 14, 0.95), Span('email', 8, 12, 0.9),
                                  Span('phone', 20, 22, 0.99)])


@unittest.skipUnless((MODEL_DIR / 'model_int8.onnx').exists(), 'fixed M0 model resource not downloaded')
class RealModelSmokeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        from tacet_detect_astrlink.detector import Detector
        manifest = json.loads((ROOT / 'resources/models/astrlink-guard-int8.json').read_text())
        cls.detector = Detector(MODEL_DIR, manifest)

    def spans(self, text):
        return [(s.label, text[s.start:s.end]) for s in self.detector.detect(text) if s.score >= 0.8]

    def test_model_card_examples_map_to_exact_code_point_spans(self):
        text = '请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。'
        found = dict((label, value) for label, value in self.spans(text))
        self.assertEqual(found.get('private_person'), '张伟')
        self.assertEqual(found.get('phone'), '13812345678')
        self.assertEqual(found.get('email'), 'zhangwei@example.com')

    def test_matches_model_card_reference_decoder_on_real_output(self):
        """Differential test against the reference snippet published in the model card."""
        import numpy as np
        from tacet_detect_astrlink.decode import decode_window
        id2label = json.loads((MODEL_DIR / 'labels.json').read_text())['id2label']
        texts = [
            '请联系张伟，电话 13812345678，邮箱 zhangwei@example.com，收货地址：北京市海淀区中关村大街 27 号。',
            'Contact Jane Doe at jane.doe@example.org or +1 415-555-0132. Server 10.0.3.17, '
            'api_key = "q7Vt2LmX9pRk4sWz8bNc3yHd6fJg1aE5"',
            '订单号 202409301234567890，金额 128.00 元，已于 2026-09-30 14:05 发货。',
            '王芳的生日是 1992 年 7 月 8 日，护照有效期至 2031-05-01。',
            '[2026-09-30 14:05:12] INFO request served in 35 ms',
            '😀😀 联系 Jane Doe，邮箱 jane.doe@example.org，访问 https://example.com/a?b=1 和 192.168.1.20:8080',
        ]
        for text in texts:
            with self.subTest(text=text[:20]):
                encoded = self.detector._tokenizer.encode(text)
                ids = np.array([encoded.ids], dtype=np.int64)
                feed = {'input_ids': ids, 'attention_mask': np.ones_like(ids), 'token_type_ids': np.zeros_like(ids)}
                logits = self.detector._session.run(['logits'], feed)[0][0]
                probs = np.exp(logits - logits.max(-1, keepdims=True))
                probs /= probs.sum(-1, keepdims=True)
                expected, current = [], None
                for label_id, p, (start, end) in zip(probs.argmax(-1), probs.max(-1), encoded.offsets):
                    if start == end:
                        continue
                    tag, _, kind = id2label[str(label_id)].partition('-')
                    if tag == 'O':
                        current = None
                    elif tag == 'B' or current is None or current['label'] != kind:
                        current = {'label': kind, 'start': start, 'end': end, 'score': float(p)}
                        expected.append(current)
                    else:
                        current['end'] = end
                        current['score'] = min(current['score'], float(p))
                actual = decode_window(len(text), encoded.offsets, logits.tolist())
                self.assertEqual([(s.label, s.start, s.end) for s in actual],
                                 [(e['label'], e['start'], e['end']) for e in expected])
                for got, want in zip(actual, expected):
                    self.assertAlmostEqual(got.score, want['score'], places=5)

    def test_model_card_published_spans(self):
        found = self.spans('王芳的生日是 1992 年 7 月 8 日，护照有效期至 2031-05-01。')
        self.assertEqual(found, [('private_person', '王芳'), ('private_date', '1992 年 7 月 8 日'),
                                 ('private_date', '2031-05-01')])
        found = self.spans('请联系张伟，电话 13812345678，邮箱 zhangwei@example.com，收货地址：北京市海淀区中关村大街 27 号。')
        self.assertEqual(found[-1], ('private_address', '北京市海淀区中关村大街 27 号'))
        self.assertEqual(len(found), 4)

    def test_non_personal_text_has_no_spans(self):
        self.assertEqual(self.spans('[2026-09-30 14:05:12] INFO request served in 35 ms'), [])

    def test_astral_characters_do_not_shift_offsets(self):
        text = '😀😀 联系 Jane Doe，邮箱 jane.doe@example.org'
        found = dict((label, value) for label, value in self.spans(text))
        self.assertEqual(found.get('email'), 'jane.doe@example.org')

    def test_entity_across_window_boundary_is_found_once(self):
        text = '公开示例 document ' * 1000 + '联系人张伟，电话 13812345678。'
        phones = [v for l, v in self.spans(text) if l == 'phone']
        self.assertEqual(phones, ['13812345678'])


if __name__ == '__main__':
    unittest.main()
