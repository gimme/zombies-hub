// web/src/adpcm.ts - decodes MS-ADPCM, the format the game keeps its streamed sounds in. Browsers don't play it.

/** A decoded sound: the samples of each channel, from -1 to 1, and the number of samples a second. */
export interface Pcm {
  channels: Float32Array<ArrayBuffer>[];
  rate: number;
}

/**
 * Decodes a WAV file of MS-ADPCM (WAVE_FORMAT_ADPCM). Its data is in blocks. A block starts with a predictor, a delta
 * and two samples for each channel. 4-bit differences follow, one channel in turn.
 */
export function decodeAdpcm(wav: ArrayBuffer): Pcm {
  const dv = new DataView(wav);
  const tag = (at: number) =>
    String.fromCharCode(dv.getUint8(at), dv.getUint8(at + 1), dv.getUint8(at + 2), dv.getUint8(at + 3));
  if (tag(0) !== 'RIFF' || tag(8) !== 'WAVE') throw new Error('not a WAV');
  let fmt = -1, data = -1, dataSize = 0;
  for (let at = 12; at + 8 <= wav.byteLength;) {
    const id = tag(at), size = dv.getUint32(at + 4, true);
    if (id === 'fmt ') fmt = at + 8;
    else if (id === 'data') {
      data = at + 8;
      dataSize = Math.min(size, wav.byteLength - data);
    }
    at += 8 + size + (size & 1);
  }
  if (fmt < 0 || data < 0 || dv.getUint16(fmt, true) !== 2) throw new Error('not MS-ADPCM');
  const channels = dv.getUint16(fmt + 2, true), rate = dv.getUint32(fmt + 4, true);
  const blockAlign = dv.getUint16(fmt + 12, true), samplesPerBlock = dv.getUint16(fmt + 18, true);
  const coefCount = dv.getUint16(fmt + 20, true);
  const coef: [number, number][] = [];
  for (let i = 0; i < coefCount; i++) {
    coef.push([dv.getInt16(fmt + 22 + i * 4, true), dv.getInt16(fmt + 24 + i * 4, true)]);
  }
  const adapt = [230, 230, 230, 230, 307, 409, 512, 614, 768, 614, 512, 409, 307, 230, 230, 230];
  const blocks = Math.floor(dataSize / blockAlign);
  const out = Array.from({ length: channels }, () => new Float32Array(blocks * samplesPerBlock));
  const clamp = (v: number) => (v < -32768 ? -32768 : v > 32767 ? 32767 : v);
  const pred = new Array<[number, number]>(channels);
  const delta = new Array<number>(channels), s1 = new Array<number>(channels), s2 = new Array<number>(channels);
  for (let b = 0; b < blocks; b++) {
    let at = data + b * blockAlign;
    const frame = b * samplesPerBlock;
    for (let c = 0; c < channels; c++) pred[c] = coef[Math.min(dv.getUint8(at++), coef.length - 1)];
    for (let c = 0; c < channels; c++, at += 2) delta[c] = dv.getInt16(at, true);
    for (let c = 0; c < channels; c++, at += 2) s1[c] = dv.getInt16(at, true);
    for (let c = 0; c < channels; c++, at += 2) s2[c] = dv.getInt16(at, true);
    for (let c = 0; c < channels; c++) {
      out[c][frame] = s2[c] / 32768;
      out[c][frame + 1] = s1[c] / 32768;
    }
    const end = data + (b + 1) * blockAlign;
    for (let i = 2 * channels, high = true; i < samplesPerBlock * channels && at < end; i++) {
      const byte = dv.getUint8(at);
      const n = high ? byte >> 4 : byte & 15;
      if (!high) at++;
      high = !high;
      const c = i % channels, signed = n >= 8 ? n - 16 : n;
      const sample = clamp(((s1[c] * pred[c][0] + s2[c] * pred[c][1]) >> 8) + signed * delta[c]);
      s2[c] = s1[c];
      s1[c] = sample;
      delta[c] = Math.max(16, (adapt[n] * delta[c]) >> 8);
      out[c][frame + Math.floor(i / channels)] = sample / 32768;
    }
  }
  return { channels: out, rate };
}
