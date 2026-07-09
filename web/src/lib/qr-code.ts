type QRBlockGroup = {
  count: number;
  dataCodewords: number;
};

type QRVersionSpec = {
  version: number;
  dataCodewords: number;
  ecCodewordsPerBlock: number;
  blocks: QRBlockGroup[];
  alignment: number[];
};

const lowErrorCorrectionSpecs: QRVersionSpec[] = [
  { version: 1, dataCodewords: 19, ecCodewordsPerBlock: 7, blocks: [{ count: 1, dataCodewords: 19 }], alignment: [] },
  { version: 2, dataCodewords: 34, ecCodewordsPerBlock: 10, blocks: [{ count: 1, dataCodewords: 34 }], alignment: [6, 18] },
  { version: 3, dataCodewords: 55, ecCodewordsPerBlock: 15, blocks: [{ count: 1, dataCodewords: 55 }], alignment: [6, 22] },
  { version: 4, dataCodewords: 80, ecCodewordsPerBlock: 20, blocks: [{ count: 1, dataCodewords: 80 }], alignment: [6, 26] },
  { version: 5, dataCodewords: 108, ecCodewordsPerBlock: 26, blocks: [{ count: 1, dataCodewords: 108 }], alignment: [6, 30] },
  { version: 6, dataCodewords: 136, ecCodewordsPerBlock: 18, blocks: [{ count: 2, dataCodewords: 68 }], alignment: [6, 34] },
];

const formatMask = 0x5412;
const formatGenerator = 0x537;

export function qrCodeDataURL(value: string) {
  const svg = qrCodeSVG(value);
  return svg ? `data:image/svg+xml;charset=UTF-8,${encodeURIComponent(svg)}` : "";
}

export function qrCodeSVG(value: string) {
  const matrix = encodeQRCode(value);
  if (!matrix) return "";
  const border = 4;
  const size = matrix.length + border * 2;
  const path = matrix
    .flatMap((row, y) => row.map((cell, x) => (cell ? `M${x + border} ${y + border}h1v1h-1z` : "")))
    .filter(Boolean)
    .join("");
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}" width="${size}" height="${size}" shape-rendering="crispEdges"><rect width="100%" height="100%" fill="#fff"/><path fill="#000" d="${path}"/></svg>`;
}

function encodeQRCode(value: string) {
  const bytes = [...new TextEncoder().encode(value)];
  const spec = selectVersionSpec(bytes.length);
  if (!spec) return null;
  const dataCodewords = makeDataCodewords(bytes, spec);
  const allCodewords = addErrorCorrection(dataCodewords, spec);
  const dataBits = allCodewords.flatMap((byte) => byteToBits(byte));
  const base = makeBaseMatrix(spec);
  let bestMatrix: boolean[][] | null = null;
  let bestPenalty = Number.POSITIVE_INFINITY;

  for (let mask = 0; mask < 8; mask += 1) {
    const candidate = cloneMatrix(base.modules);
    placeDataBits(candidate, base.reserved, dataBits, mask);
    writeFormatBits(candidate, mask);
    const penalty = qrPenalty(candidate);
    if (penalty < bestPenalty) {
      bestPenalty = penalty;
      bestMatrix = candidate;
    }
  }

  return bestMatrix;
}

function selectVersionSpec(byteLength: number) {
  return lowErrorCorrectionSpecs.find((spec) => {
    const countBits = spec.version < 10 ? 8 : 16;
    return byteLength < 1 << countBits && 4 + countBits + byteLength * 8 <= spec.dataCodewords * 8;
  });
}

function makeDataCodewords(bytes: number[], spec: QRVersionSpec) {
  const countBits = spec.version < 10 ? 8 : 16;
  const bits: number[] = [];
  appendBits(bits, 0b0100, 4);
  appendBits(bits, bytes.length, countBits);
  for (const byte of bytes) appendBits(bits, byte, 8);
  const capacityBits = spec.dataCodewords * 8;
  appendBits(bits, 0, Math.min(4, capacityBits - bits.length));
  while (bits.length % 8 !== 0) bits.push(0);

  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    data.push(bits.slice(i, i + 8).reduce((acc, bit) => (acc << 1) | bit, 0));
  }
  for (let pad = 0xec; data.length < spec.dataCodewords; pad = pad === 0xec ? 0x11 : 0xec) {
    data.push(pad);
  }
  return data;
}

function appendBits(bits: number[], value: number, length: number) {
  for (let i = length - 1; i >= 0; i -= 1) bits.push((value >>> i) & 1);
}

function byteToBits(byte: number) {
  const bits: number[] = [];
  appendBits(bits, byte, 8);
  return bits;
}

function addErrorCorrection(data: number[], spec: QRVersionSpec) {
  const blocks: number[][] = [];
  let offset = 0;
  for (const group of spec.blocks) {
    for (let i = 0; i < group.count; i += 1) {
      blocks.push(data.slice(offset, offset + group.dataCodewords));
      offset += group.dataCodewords;
    }
  }
  const ecBlocks = blocks.map((block) => reedSolomonRemainder(block, spec.ecCodewordsPerBlock));
  const out: number[] = [];
  const maxDataLength = Math.max(...blocks.map((block) => block.length));
  for (let i = 0; i < maxDataLength; i += 1) {
    for (const block of blocks) {
      if (i < block.length) out.push(block[i]);
    }
  }
  for (let i = 0; i < spec.ecCodewordsPerBlock; i += 1) {
    for (const block of ecBlocks) out.push(block[i]);
  }
  return out;
}

const gfExp: number[] = [];
const gfLog: number[] = [];
let gfValue = 1;
for (let i = 0; i < 255; i += 1) {
  gfExp[i] = gfValue;
  gfLog[gfValue] = i;
  gfValue <<= 1;
  if (gfValue & 0x100) gfValue ^= 0x11d;
}
for (let i = 255; i < 512; i += 1) gfExp[i] = gfExp[i - 255];

function gfMultiply(a: number, b: number) {
  if (a === 0 || b === 0) return 0;
  return gfExp[gfLog[a] + gfLog[b]];
}

function reedSolomonGenerator(degree: number) {
  let poly = [1];
  for (let i = 0; i < degree; i += 1) {
    const next = Array(poly.length + 1).fill(0) as number[];
    for (let j = 0; j < poly.length; j += 1) {
      next[j] ^= poly[j];
      next[j + 1] ^= gfMultiply(poly[j], gfExp[i]);
    }
    poly = next;
  }
  return poly;
}

function reedSolomonRemainder(data: number[], degree: number) {
  const generator = reedSolomonGenerator(degree);
  const result = Array(degree).fill(0) as number[];
  for (const byte of data) {
    const factor = byte ^ result[0];
    result.shift();
    result.push(0);
    for (let i = 0; i < degree; i += 1) {
      result[i] ^= gfMultiply(generator[i + 1], factor);
    }
  }
  return result;
}

function makeBaseMatrix(spec: QRVersionSpec) {
  const size = spec.version * 4 + 17;
  const modules = Array.from({ length: size }, () => Array(size).fill(false) as boolean[]);
  const reserved = Array.from({ length: size }, () => Array(size).fill(false) as boolean[]);
  const set = (x: number, y: number, value: boolean) => {
    if (x < 0 || y < 0 || x >= size || y >= size) return;
    modules[y][x] = value;
    reserved[y][x] = true;
  };

  drawFinderPattern(set, 3, 3);
  drawFinderPattern(set, size - 4, 3);
  drawFinderPattern(set, 3, size - 4);
  for (let i = 8; i < size - 8; i += 1) {
    set(i, 6, i % 2 === 0);
    set(6, i, i % 2 === 0);
  }
  for (const y of spec.alignment) {
    for (const x of spec.alignment) {
      if ((x <= 8 && y <= 8) || (x >= size - 9 && y <= 8) || (x <= 8 && y >= size - 9)) continue;
      drawAlignmentPattern(set, x, y);
    }
  }
  set(8, size - 8, true);
  reserveFormatModules(reserved);
  return { modules, reserved };
}

function drawFinderPattern(set: (x: number, y: number, value: boolean) => void, centerX: number, centerY: number) {
  for (let y = -4; y <= 4; y += 1) {
    for (let x = -4; x <= 4; x += 1) {
      const distance = Math.max(Math.abs(x), Math.abs(y));
      set(centerX + x, centerY + y, distance !== 2 && distance !== 4);
    }
  }
}

function drawAlignmentPattern(set: (x: number, y: number, value: boolean) => void, centerX: number, centerY: number) {
  for (let y = -2; y <= 2; y += 1) {
    for (let x = -2; x <= 2; x += 1) {
      set(centerX + x, centerY + y, Math.max(Math.abs(x), Math.abs(y)) !== 1);
    }
  }
}

function reserveFormatModules(reserved: boolean[][]) {
  const size = reserved.length;
  for (let i = 0; i <= 8; i += 1) {
    reserved[8][i] = true;
    reserved[i][8] = true;
  }
  for (let i = 0; i < 8; i += 1) {
    reserved[8][size - 1 - i] = true;
    reserved[size - 1 - i][8] = true;
  }
}

function placeDataBits(modules: boolean[][], reserved: boolean[][], bits: number[], mask: number) {
  const size = modules.length;
  let bitIndex = 0;
  let upward = true;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right -= 1;
    for (let vert = 0; vert < size; vert += 1) {
      const y = upward ? size - 1 - vert : vert;
      for (let dx = 0; dx < 2; dx += 1) {
        const x = right - dx;
        if (reserved[y][x]) continue;
        const bit = bitIndex < bits.length ? bits[bitIndex] === 1 : false;
        modules[y][x] = maskBit(mask, x, y) ? !bit : bit;
        bitIndex += 1;
      }
    }
    upward = !upward;
  }
}

function maskBit(mask: number, x: number, y: number) {
  switch (mask) {
    case 0:
      return (x + y) % 2 === 0;
    case 1:
      return y % 2 === 0;
    case 2:
      return x % 3 === 0;
    case 3:
      return (x + y) % 3 === 0;
    case 4:
      return (Math.floor(y / 2) + Math.floor(x / 3)) % 2 === 0;
    case 5:
      return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6:
      return (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    default:
      return (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
  }
}

function writeFormatBits(modules: boolean[][], mask: number) {
  const size = modules.length;
  const bits = formatBits(mask);
  const bit = (index: number) => ((bits >>> index) & 1) !== 0;
  for (let i = 0; i <= 5; i += 1) modules[i][8] = bit(i);
  modules[7][8] = bit(6);
  modules[8][8] = bit(7);
  modules[8][7] = bit(8);
  for (let i = 9; i < 15; i += 1) modules[8][14 - i] = bit(i);
  for (let i = 0; i < 8; i += 1) modules[8][size - 1 - i] = bit(i);
  for (let i = 8; i < 15; i += 1) modules[size - 15 + i][8] = bit(i);
  modules[size - 8][8] = true;
}

function formatBits(mask: number) {
  const data = (1 << 3) | mask;
  let bits = data << 10;
  for (let i = 14; i >= 10; i -= 1) {
    if (((bits >>> i) & 1) !== 0) bits ^= formatGenerator << (i - 10);
  }
  return ((data << 10) | bits) ^ formatMask;
}

function cloneMatrix(matrix: boolean[][]) {
  return matrix.map((row) => [...row]);
}

function qrPenalty(matrix: boolean[][]) {
  return adjacentPenalty(matrix) + blockPenalty(matrix) + finderLikePenalty(matrix) + balancePenalty(matrix);
}

function adjacentPenalty(matrix: boolean[][]) {
  const linePenalty = (line: boolean[]) => {
    let penalty = 0;
    let runColor = line[0];
    let runLength = 1;
    for (let i = 1; i < line.length; i += 1) {
      if (line[i] === runColor) {
        runLength += 1;
      } else {
        if (runLength >= 5) penalty += runLength - 2;
        runColor = line[i];
        runLength = 1;
      }
    }
    if (runLength >= 5) penalty += runLength - 2;
    return penalty;
  };
  let penalty = matrix.reduce((sum, row) => sum + linePenalty(row), 0);
  for (let x = 0; x < matrix.length; x += 1) {
    penalty += linePenalty(matrix.map((row) => row[x]));
  }
  return penalty;
}

function blockPenalty(matrix: boolean[][]) {
  let penalty = 0;
  for (let y = 0; y < matrix.length - 1; y += 1) {
    for (let x = 0; x < matrix.length - 1; x += 1) {
      const color = matrix[y][x];
      if (matrix[y][x + 1] === color && matrix[y + 1][x] === color && matrix[y + 1][x + 1] === color) penalty += 3;
    }
  }
  return penalty;
}

function finderLikePenalty(matrix: boolean[][]) {
  const pattern = [true, false, true, true, true, false, true, false, false, false, false];
  const reverse = [...pattern].reverse();
  const linePenalty = (line: boolean[]) => {
    let penalty = 0;
    for (let i = 0; i <= line.length - pattern.length; i += 1) {
      const segment = line.slice(i, i + pattern.length);
      if (samePattern(segment, pattern) || samePattern(segment, reverse)) penalty += 40;
    }
    return penalty;
  };
  let penalty = matrix.reduce((sum, row) => sum + linePenalty(row), 0);
  for (let x = 0; x < matrix.length; x += 1) {
    penalty += linePenalty(matrix.map((row) => row[x]));
  }
  return penalty;
}

function samePattern(values: boolean[], pattern: boolean[]) {
  return values.every((value, index) => value === pattern[index]);
}

function balancePenalty(matrix: boolean[][]) {
  const total = matrix.length * matrix.length;
  const dark = matrix.reduce((sum, row) => sum + row.filter(Boolean).length, 0);
  const percent = (dark * 100) / total;
  return Math.floor(Math.abs(percent - 50) / 5) * 10;
}
