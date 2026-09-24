package main

import (
	"log/slog"
	"net"
	"sync"

	"wacalls/internal/voip/call"
	"wacalls/internal/voip/media"
)

// AsteriskLeg = perna de mídia entre o motor e o Asterisk (externalMedia), no
// lugar da perna do browser. Ponte de PCM: o áudio do WhatsApp (OnPeerAudio,
// float32 16kHz) sai como RTP μ-law 8kHz pro Asterisk; o RTP μ-law que volta do
// Asterisk vira PCM 16kHz e entra no WhatsApp (FeedCapturedPCM).
//
// Formato = ulaw (PT=0, 8kHz, 20ms/160 samples) — provado no PoC Fase 1 do agxone
// (slin16/PT dinâmico NÃO é lido na injeção pelo Asterisk; ulaw/PT=0 funciona).
type AsteriskLeg struct {
	conn   *net.UDPConn
	remote *net.UDPAddr
	rtp    *media.RtpSession
	cm     *call.CallManager
	log    *slog.Logger

	mu      sync.Mutex
	residue []float32 // sobra @8k p/ empacotar em frames de 160 samples
	closed  bool
	stop    chan struct{}
}

const asteriskSamplesPerPacket = 160 // 20ms @ 8kHz

// NewAsteriskLeg abre o socket UDP e começa a ouvir o RTP do Asterisk.
// asteriskHost:asteriskPort = UNICASTRTP_LOCAL_ADDRESS/PORT do canal externalMedia
// (pra onde ENVIAMOS). localPort = porta que ESCUTAMOS — tem que ser a MESMA que o
// backend passou como `external_host` ao criar o externalMedia (RTP simétrico:
// enviamos e recebemos pela mesma porta). localPort=0 = aleatória (só se o Asterisk
// tiver rtp_symmetric e aprender a fonte).
func NewAsteriskLeg(asteriskHost string, asteriskPort, localPort int, cm *call.CallManager, log *slog.Logger) (*AsteriskLeg, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: localPort})
	if err != nil {
		return nil, err
	}
	remote := &net.UDPAddr{IP: net.ParseIP(asteriskHost), Port: asteriskPort}
	leg := &AsteriskLeg{
		conn:   conn,
		remote: remote,
		rtp:    media.NewRtpSession(0x1a2b3c4d, 0 /*ulaw PT*/, 8000, asteriskSamplesPerPacket),
		cm:     cm,
		log:    log,
		stop:   make(chan struct{}),
	}
	go leg.readLoop()
	log.Info("asterisk leg up", "remote", remote.String(), "local", conn.LocalAddr().String())
	return leg, nil
}

// WritePeerAudio: áudio do WhatsApp (float32 16kHz) -> RTP μ-law 8kHz pro Asterisk.
// Chamado pelo OnPeerAudio; empacota em frames de 20ms (160 samples @8k).
func (l *AsteriskLeg) WritePeerAudio(pcm16 []float32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	pcm8 := media.Downsample16to8(pcm16)
	l.residue = append(l.residue, pcm8...)
	for len(l.residue) >= asteriskSamplesPerPacket {
		frame := l.residue[:asteriskSamplesPerPacket]
		l.residue = l.residue[asteriskSamplesPerPacket:]
		ulaw := media.EncodeUlaw(frame)
		pkt := l.rtp.CreatePacket(ulaw, false)
		buf, err := pkt.Encode()
		if err != nil {
			continue
		}
		if _, err := l.conn.WriteToUDP(buf, l.remote); err != nil {
			return
		}
	}
}

// readLoop: RTP μ-law do Asterisk -> PCM 16kHz -> WhatsApp (FeedCapturedPCM).
func (l *AsteriskLeg) readLoop() {
	buf := make([]byte, 2048)
	for {
		select {
		case <-l.stop:
			return
		default:
		}
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt, err := media.DecodeRtpPacket(buf[:n])
		if err != nil || len(pkt.Payload) == 0 {
			continue
		}
		pcm8 := media.DecodeUlaw(pkt.Payload)
		pcm16 := media.Upsample8to16(pcm8)
		l.cm.FeedCapturedPCM(pcm16)
	}
}

func (l *AsteriskLeg) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.mu.Unlock()
	close(l.stop)
	_ = l.conn.Close()
}
