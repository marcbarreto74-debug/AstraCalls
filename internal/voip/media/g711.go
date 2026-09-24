package media

// G.711 μ-law (PCMU) + resample 16kHz<->8kHz.
// Usado na perna de mídia p/ o Asterisk (externalMedia format=ulaw, PT=0, 8kHz):
// o slin16 (PT dinâmico) NÃO é lido na injeção pelo Asterisk — ulaw (well-known
// PT=0) funciona. Ver CLAUDE.md do agxone (PoC Fase 1 do bridge WhatsApp->ramal).

// linToUlaw converte uma amostra PCM 16-bit em um byte μ-law.
func linToUlaw(sample int16) byte {
	const bias = 0x84
	const clip = 32635
	sign := (sample >> 8) & 0x80
	s := int(sample)
	if sign != 0 {
		s = -s
	}
	if s > clip {
		s = clip
	}
	s += bias
	exponent := 7
	expMask := 0x4000
	for (s&expMask) == 0 && exponent > 0 {
		exponent--
		expMask >>= 1
	}
	mantissa := (s >> (exponent + 3)) & 0x0f
	return byte(^(int(sign) | (exponent << 4) | mantissa) & 0xff)
}

// ulawToLin converte um byte μ-law de volta em PCM 16-bit.
func ulawToLin(u byte) int16 {
	u = ^u
	sign := int(u & 0x80)
	exponent := int((u >> 4) & 0x07)
	mantissa := int(u & 0x0f)
	sample := ((mantissa << 3) + 0x84) << exponent
	sample -= 0x84
	if sign != 0 {
		sample = -sample
	}
	return int16(sample)
}

// EncodeUlaw: PCM float32 [-1,1] @8kHz -> bytes μ-law.
func EncodeUlaw(pcm []float32) []byte {
	out := make([]byte, len(pcm))
	for i, f := range pcm {
		v := int(f * 32767)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = linToUlaw(int16(v))
	}
	return out
}

// DecodeUlaw: bytes μ-law -> PCM float32 [-1,1] @8kHz.
func DecodeUlaw(b []byte) []float32 {
	out := make([]float32, len(b))
	for i, u := range b {
		out[i] = float32(ulawToLin(u)) / 32768.0
	}
	return out
}

// Downsample16to8: 16kHz -> 8kHz (média de pares = lowpass simples anti-alias).
func Downsample16to8(in []float32) []float32 {
	out := make([]float32, len(in)/2)
	for i := range out {
		out[i] = (in[2*i] + in[2*i+1]) / 2
	}
	return out
}

// Upsample8to16: 8kHz -> 16kHz (interpolação linear).
func Upsample8to16(in []float32) []float32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]float32, len(in)*2)
	for i := 0; i < len(in); i++ {
		cur := in[i]
		var next float32
		if i+1 < len(in) {
			next = in[i+1]
		} else {
			next = cur
		}
		out[2*i] = cur
		out[2*i+1] = (cur + next) / 2
	}
	return out
}
