// SMS encoding and segments, shown as information only: the limit is 160
// characters (code points) after personalization, whatever the encoding (D2).

// GSM 03.38 basic alphabet; extension characters take two units.
const BASIC =
  '@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !"#¤%&\'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà'
const EXTENSION = '^{}\\[~]|€\f'

export type SmsInfo = { encoding: 'GSM-7' | 'UCS-2'; units: number; segments: number }

export function smsInfo(text: string): SmsInfo {
  let gsm = 0
  for (const ch of text) {
    if (BASIC.includes(ch)) gsm += 1
    else if (EXTENSION.includes(ch)) gsm += 2
    else return segmentsOf('UCS-2', text.length) // UTF-16 units: an emoji is 2
  }
  return segmentsOf('GSM-7', gsm)
}

function segmentsOf(encoding: SmsInfo['encoding'], units: number): SmsInfo {
  const [single, part] = encoding === 'GSM-7' ? [160, 153] : [70, 67]
  return { encoding, units, segments: units === 0 ? 0 : units <= single ? 1 : Math.ceil(units / part) }
}
