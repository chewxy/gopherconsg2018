package main

import (
	"flag"
	"io"
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/gomidi/midi"
	"github.com/gomidi/midi/midimessage/channel"
	"github.com/gomidi/midi/smf/smfreader"
	"github.com/rakyll/portmidi"
	pb "gopkg.in/cheggaaa/pb.v1"
	"gorgonia.org/gorgonia"
)

const (
	embeddingSize = 20
	maxOut        = 11

	// gradient update stuff
	l2reg     = 0.000001
	learnrate = 0.01
	clipVal   = 5.0
)

var trainiter = flag.Int("iter", 0, "How many iterations to train")
var toCondition = flag.Bool("condition", false, "Condition the NN to #2?")
var trainingData = flag.String("train", "simplediag.mid", "What is the MIDI file to use for training? Channel 0 is the input,  Channel 1 and above are responses")

type bridge struct {
	stream *portmidi.Stream
}

func (b *bridge) Write(msg midi.Message) (nBytes int, err error) {
	switch m := msg.(type) {
	case channel.NoteOn:
		raw := m.Raw()
		err = b.stream.WriteShort(int64(raw[0]), int64(raw[1]), int64(raw[2]))
		return len(raw), err
	case channel.NoteOff:
		raw := m.Raw()
		err = b.stream.WriteShort(int64(raw[0]), int64(raw[1]), int64(raw[2]))
		return len(raw), err
	}
	panic("Unreachable")
}

func (b *bridge) write(msgs []message, repeat bool) {
	for _, msg := range msgs {
		// for debugging
		// log.Printf("\t%v, %v, %v, %v", msg.channel, msg.key, msg.velocity, msg.duration)
		if msg.duration > 3000 {
			continue // something went wrong
		}
		if msg.key == 255 {
			time.Sleep(time.Duration(msg.duration) * time.Millisecond)
			continue
		}
		if repeat {
			ch1 := channel.New(msg.channel)
			ch2 := channel.New(msg.channel + 1)
			ch3 := channel.New(msg.channel + 2)

			updateCells(message{key: msg.key, velocity: 100})
			nOn1 := ch1.NoteOn(msg.key, msg.velocity+106)
			nOn2 := ch2.NoteOn(msg.key, msg.velocity+110)
			nOn3 := ch3.NoteOn(msg.key, msg.velocity+106)
			b.Write(nOn1)
			b.Write(nOn2)
			b.Write(nOn3)
			time.Sleep(time.Duration(msg.duration) * time.Millisecond)

			updateCells(message{key: msg.key, velocity: 0})
			nOff1 := ch1.NoteOff(msg.key)
			nOff2 := ch2.NoteOff(msg.key)
			nOff3 := ch3.NoteOff(msg.key)
			b.Write(nOff1)
			b.Write(nOff2)
			b.Write(nOff3)
		} else {
			ch := channel.New(msg.channel)

			updateCells(message{key: msg.key, velocity: 100})
			nOn := ch.NoteOn(msg.key, msg.velocity)
			b.Write(nOn)

			time.Sleep(time.Duration(msg.duration) * time.Millisecond)

			updateCells(message{key: msg.key, velocity: 0})
			nOff := ch.NoteOff(msg.key)
			b.Write(nOff)
		}
	}
}

func setupMIDIPipe() (in, out *portmidi.Stream) {
	portmidi.Initialize()
	log.Printf("Devices %v", portmidi.CountDevices())
	log.Printf("Info %v", portmidi.Info(0))
	log.Printf("Info %v", portmidi.Info(1))
	var err error

	if in, err = portmidi.NewInputStream(0, 1024); err != nil {
		log.Fatal(err)
	}

	if out, err = portmidi.NewOutputStream(1, 1024, 0); err != nil {
		log.Fatal(err)
	}
	return in, out
}

func MIDILoop(in, out *portmidi.Stream, s2s *seq2seq) {
	defer in.Close()
	defer out.Close()

	ch := in.Listen()
	var silence int64
	var timesince portmidi.Timestamp
	var msgs []message
	var cur message

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			atomic.AddInt64(&silence, 1)

		}
	}()

	b := bridge{out}

	for {
		select {
		case ev := <-ch:
			// log.Printf("RECEIVED %v %v %v", ev, byte(ev.Data1), ev.Data1)
			// input from user
			_, playChan := parseStatus(byte(ev.Status))
			key := parseData(byte(ev.Data1))
			vel := parseData(byte(ev.Data2))

			updateCells(message{key: key, velocity: vel})
			if vel == 0 {
				cur.duration = uint(ev.Timestamp - timesince)
				msgs = append(msgs, cur)
				cur = message{
					key:     255,
					channel: playChan,
				}
			} else {
				if timesince != 0 && cur.key == 255 {
					// encode rests
					cur.duration = uint(ev.Timestamp - timesince)
					msgs = append(msgs, cur)
				}
				cur.key = key
				cur.channel = playChan
				cur.velocity = vel
			}
			timesince = ev.Timestamp

			out.WriteShort(ev.Status, ev.Data1, ev.Data2)
			atomic.StoreInt64(&silence, 0)
		default:
			ts := atomic.LoadInt64(&silence)
			if ts > 2 && len(msgs) > 0 {
				// play output from computer
				pred, err := s2s.predict(msgs)
				if err != nil {
					log.Fatal(err)
				}

				b.write(pred, true)
				msgs = msgs[:0]
				atomic.StoreInt64(&silence, 0)
			}
		}
	}
}

func trainingLoop(s2s *seq2seq, iters int, pairs []trainingPair, embeddingSize, hiddenSize int, keys []byte, durations []uint, mOut *portmidi.Stream) {
	solver := gorgonia.NewRMSPropSolver(gorgonia.WithLearnRate(learnrate), gorgonia.WithL2Reg(l2reg), gorgonia.WithClip(clipVal))
	bar := pb.StartNew(iters)

	for i := 0; i < iters; i++ {
		if err := train(s2s, i, solver, pairs[:]); err != nil && err != io.EOF {
			log.Fatalf("Training Failure %+v", err)
		}
		bar.Increment()
		if i%100 == 0 && i > 0 {
			s2s.g.UnbindAll()

			s2s = NewS2S(embeddingSize, hiddenSize, keys, durations)
			runtime.GC() // reduce memory pressure
			if err := s2s.load(); err != nil {
				log.Fatalf("GC Pressure Reduction Failure", err)
			}
		}
	}
	if iters > 50 {
		if err := s2s.checkpoint(); err != nil {
			log.Fatalf("Failed to save checkpoint after training", err)
		}
	}
	bar.Finish()

	// notify user that the neural network is ready
	mOut.WriteShort(0x90, int64(keys[0]), 100)
	mOut.WriteShort(0x90, int64(keys[len(keys)-1]), 100)
	time.Sleep(1 * time.Second)
	mOut.WriteShort(0x80, int64(keys[0]), 0)
	mOut.WriteShort(0x80, int64(keys[len(keys)-1]), 0)
}

func main() {
	flag.Parse()
	mIn, mOut := setupMIDIPipe()

	d := new(decoder)
	if err := smfreader.ReadFile(*trainingData, d.readMIDI); err != nil {
		log.Fatal(err)
	}

	pairs, keys, durations := d.makeTrainingPairs(*toCondition)
	log.Printf("%d Pairs | %v", len(pairs), pairs[0].in)
	log.Printf("Keys %v", keys)
	log.Printf("Durations %v", durations)

	start := keys[0]
	for i := 0; i < len(keys); i++ {
		key := keys[i]

		loc := int(key - start)
		if loc <= 1 {
			if _, ok := cellLookup[key]; ok {
				continue
			}
			y := loc / cols
			x := loc % cols

			cellLookup[key] = struct{ x, y int }{x, y}
		} else {
			// fill in the missing keya so that when we press on them in the demo, accidentally, they light up
			for j := loc; j >= 0; j-- {
				key2 := key - byte(j)
				if _, ok := cellLookup[key2]; ok {
					continue
				}
				loc2 := int(key2 - start)
				y := loc2 / cols
				x := loc2 % cols
				cellLookup[key2] = struct{ x, y int }{x, y}
			}
		}
	}

	// log.Printf("CellLookup %v", cellLookup)

	// fwd upper bound, good as a guideline but otherwise useless
	// hiddenSize := len(pairs) / (2 * (2*embeddingSize + len(keys) + len(durations)))
	// if hiddenSize == 0 {
	// 	hiddenSize = 100
	// }
	hiddenSize := 100

	s2s := NewS2S(embeddingSize, hiddenSize, keys, durations)

	// try to load
	var iters = *trainiter
	if err := s2s.load(); err != nil {
		log.Printf("Loading failed %v", err)
		iters = 10000
	}

	go trainingLoop(s2s, iters, pairs, embeddingSize, hiddenSize, keys, durations, mOut)
	go MIDILoop(mIn, mOut, s2s)
	mainGL()

}
