import { expect, test } from 'vitest';
import { decodeAdpcm } from './adpcm';

// the coefficients that every MS-ADPCM file has
const COEFS = [[256, 0], [512, -256], [0, 0], [192, 64], [240, 0], [460, -208], [392, -232]];

// A WAV file of MS-ADPCM with these blocks of data.
function wav(channels: number, rate: number, blockAlign: number, samplesPerBlock: number, blocks: number[][],
  format = 2) {
  const fmtSize = 22 + 4 * COEFS.length;
  const data = blocks.flat();
  const dv = new DataView(new ArrayBuffer(12 + 8 + fmtSize + 8 + data.length));
  const tag = (at: number, text: string) => [...text].forEach((c, i) => dv.setUint8(at + i, c.charCodeAt(0)));
  tag(0, 'RIFF');
  dv.setUint32(4, dv.byteLength - 8, true);
  tag(8, 'WAVE');
  tag(12, 'fmt ');
  dv.setUint32(16, fmtSize, true);
  const fmt = 20;
  dv.setUint16(fmt, format, true);
  dv.setUint16(fmt + 2, channels, true);
  dv.setUint32(fmt + 4, rate, true);
  dv.setUint16(fmt + 12, blockAlign, true);
  dv.setUint16(fmt + 14, 4, true); // bits a sample
  dv.setUint16(fmt + 16, 4 + 4 * COEFS.length, true);
  dv.setUint16(fmt + 18, samplesPerBlock, true);
  dv.setUint16(fmt + 20, COEFS.length, true);
  COEFS.forEach(([a, b], i) => {
    dv.setInt16(fmt + 22 + i * 4, a, true);
    dv.setInt16(fmt + 24 + i * 4, b, true);
  });
  tag(fmt + fmtSize, 'data');
  dv.setUint32(fmt + fmtSize + 4, data.length, true);
  data.forEach((byte, i) => dv.setUint8(fmt + fmtSize + 8 + i, byte));
  return dv.buffer;
}
const int16 = (v: number) => [v & 255, (v >> 8) & 255];
const samples = (pcm: Float32Array) => [...pcm].map((s) => Math.round(s * 32768));

test('a mono block: its two first samples, then each difference added to the prediction', () => {
  // predictor 0 predicts the last sample. The delta is 16, so the differences +1 and -1 are +16 and -16.
  const block = [0, ...int16(16), ...int16(1000), ...int16(500), 0x1f];
  const pcm = decodeAdpcm(wav(1, 22050, 8, 4, [block]));
  expect(pcm.rate).toBe(22050);
  expect(pcm.channels.length).toBe(1);
  expect(samples(pcm.channels[0])).toEqual([500, 1000, 1016, 1000]);
});

test('the delta grows after a large difference', () => {
  // +7 is the largest difference: 7 * 16 = 112, and the delta becomes 16 * 614 / 256 = 38
  const block = [0, ...int16(16), ...int16(0), ...int16(0), 0x71];
  expect(samples(decodeAdpcm(wav(1, 8000, 8, 4, [block])).channels[0])).toEqual([0, 0, 112, 150]);
});

test('a stereo block has the channels in turn, and blocks follow each other', () => {
  const block = (left: number, right: number) =>
    [0, 0, ...int16(16), ...int16(16), ...int16(left), ...int16(right), ...int16(left), ...int16(right), 0x1f];
  const pcm = decodeAdpcm(wav(2, 44100, 15, 3, [block(100, -100), block(200, -200)]));
  expect(samples(pcm.channels[0])).toEqual([100, 100, 116, 200, 200, 216]);
  expect(samples(pcm.channels[1])).toEqual([-100, -100, -116, -200, -200, -216]);
});

test('a sample stays within 16 bits', () => {
  const block = [0, ...int16(2000), ...int16(32000), ...int16(32000), 0x70];
  expect(samples(decodeAdpcm(wav(1, 8000, 8, 4, [block])).channels[0])[2]).toBe(32767);
});

test('what is not MS-ADPCM is refused', () => {
  expect(() => decodeAdpcm(new ArrayBuffer(64))).toThrow('not a WAV');
  expect(() => decodeAdpcm(wav(1, 8000, 8, 4, [], 1))).toThrow('not MS-ADPCM');
});
