package main

import (
	"log"
	"math/rand"
	"os"
	"sort"
	"unsafe"

	"github.com/gomidi/midi/midimessage/channel"
	"github.com/gomidi/midi/midimessage/meta"
	"github.com/gomidi/midi/smf"
	"github.com/gomidi/midi/smf/smftrack"
	"github.com/gomidi/midi/smf/smfwriter"
	"github.com/xtgo/set"
)

type channeler interface {
	Channel() byte
}

var tpq = smf.MetricTicks(480) // set the time resolution in ticks per quarter note; 0 uses the defaults (i.e. 960)

type message struct {
	channel  byte
	key      byte
	duration uint
	velocity byte // velocity 0 == noteoff
}

type trainingPair struct {
	in, out []message
}

type decoder struct {
	msgs smftrack.Events
	err  error
}

func (d *decoder) readMIDI(rd smf.Reader) {
	tpq = rd.Header().TimeFormat.(smf.MetricTicks)
	log.Printf("%v | %v | %v", tpq, rd.Delta(), rd.Header().Type())
	v1 := smftrack.SMF1{}
	var tracks []*smftrack.Track
	tracks, d.err = v1.ReadFrom(rd)

	// var i int
	var t *smftrack.Track
	each := func(e smftrack.Event) {
		// log.Printf("Track %d. Message %v", i, e)
		if c, ok := e.Message.(channeler); ok && (c.Channel() == 1 || c.Channel() == 0) {
			d.msgs = append(d.msgs, e)
		}
	}
	for _, t = range tracks {
		t.EachEvent(each)
	}

}

func (d *decoder) makeTrainingPairs(condition bool) (retVal []trainingPair, keys []byte, durations []uint) {
	sort.Sort(d.msgs)
	var cur byte
	var p trainingPair
	for i, ev := range d.msgs {
		switch msg := ev.Message.(type) {
		case channel.NoteOn:
			m := message{
				channel:  msg.Channel(),
				key:      msg.Key(),
				velocity: msg.Velocity(),
			}

			for _, ev2 := range d.msgs[i+1:] {
				if noff, ok := ev2.Message.(channel.NoteOff); ok && noff.Key() == msg.Key() {
					m.duration = uint(ev2.AbsTicks - ev.AbsTicks)
					break
				}
			}
			keys = append(keys, m.key)
			durations = append(durations, m.duration)

			switch {
			case m.channel == cur && cur == 0:
				p.in = append(p.in, m)
			case m.channel == cur && cur == 1:
				p.out = append(p.out, m)
			case m.channel != cur && cur == 0:
				cur = m.channel
				p.out = append(p.out, m)
			case m.channel != cur && cur == 1:
				cur = m.channel
				retVal = append(retVal, p)
				p = trainingPair{
					in: []message{m},
				}
			}

		case channel.NoteOff:
			m := message{
				channel:  msg.Channel(),
				key:      255,
				velocity: 0,
			}
			for _, ev2 := range d.msgs[i+1:] {
				if _, ok := ev2.Message.(channel.NoteOn); ok {
					m.duration = uint(ev2.AbsTicks - ev.AbsTicks)
					break
				}
			}
			switch {
			case m.channel == cur && cur == 0:
				p.in = append(p.in, m)
			case m.channel == cur && cur == 1:
				p.out = append(p.out, m)
			case m.channel != cur && cur == 0:
				cur = m.channel
				p.out = append(p.out, m)
			case m.channel != cur && cur == 1:
				cur = m.channel
				retVal = append(retVal, p)
				p = trainingPair{
					in: []message{m},
				}
			}
		}
	}
	if len(p.in) > 0 && len(p.out) > 0 {
		retVal = append(retVal, p)
	}

	sort.Sort(byteslice(keys))
	sort.Sort(uintslice(durations))

	n := set.Uniq(byteslice(keys))
	keys = keys[:n]

	n = set.Uniq(uintslice(durations))
	durations = durations[:n]

	if condition {
		log.Printf("retVal %d", len(retVal))
		retVal = retVal[0:1]
		for i := range retVal {
			p := retVal[i]
			// for _, dur := range durations {
			// 	if dur > 900 {
			// 		continue
			// 	}

			// 	// change one input durations
			// 	for j := range p.in {
			// 		newIn := make([]message, len(p.in))
			// 		copy(newIn, p.in)
			// 		newIn[j].duration = dur
			// 		newP := trainingPair{in: newIn, out: p.out}
			// 		retVal = append(retVal, newP)
			// 	}
			// }

			for count := 0; count < 10; count++ {
				// random select
				var dur uint
				for dur > 900 || dur == 0 {
					randDur := rand.Intn(len(durations))
					dur = durations[randDur]
				}
				randInput := rand.Intn(len(p.in))
				newIn := make([]message, len(p.in))
				copy(newIn, p.in)
				newIn[randInput].duration = dur
				newP := trainingPair{in: newIn, out: p.out}
				retVal = append(retVal, newP)
			}
		}

		return retVal, keys, durations
	}

	// make additional training pairs which will heavily bias against sample 2 on purpose for the purpose of the demonstration
	curr := len(retVal) - 1 // ignore the last sequence, which is a bonus sequence to learn
	for i := 1; i < curr; i++ {
		p := retVal[i]

		if i == 2 {
			for _, dur := range durations {
				if dur > 900 {
					continue
				}

				// change one input durations
				for j := range p.in {
					newIn := make([]message, len(p.in))
					copy(newIn, p.in)
					newIn[j].duration = dur
					newP := trainingPair{in: newIn, out: p.out}
					retVal = append(retVal, newP)
				}
			}
		}
		for count := 0; count < 5; count++ {
			// random select
			var dur uint
			for dur > 900 || dur == 0 {
				randDur := rand.Intn(len(durations))
				dur = durations[randDur]
			}
			randInput := rand.Intn(len(p.in))
			newIn := make([]message, len(p.in))
			copy(newIn, p.in)
			newIn[randInput].duration = dur
			newP := trainingPair{in: newIn, out: p.out}
			retVal = append(retVal, newP)
		}
	}
	if !condition {
		retVal = append(retVal, retVal[curr+1]) // a second copy of the bonus for shits and giggles
	}

	return retVal, keys, durations
}

func writeMidi(p trainingPair, filename string) {
	var channelIDs []byte
	for _, i := range p.in {
		channelIDs = append(channelIDs, i.channel)
	}
	for _, o := range p.out {
		channelIDs = append(channelIDs, o.channel)
	}
	sort.Sort(byteslice(channelIDs))
	n := set.Uniq(byteslice(channelIDs))
	channelIDs = channelIDs[:n]

	channels := make([]channel.Channel, len(channelIDs))
	tracks := make([]*smftrack.Track, len(channelIDs))
	m := make(map[byte]int)
	for i, id := range channelIDs {
		channels[i] = channel.New(id)
		m[id] = i
		tracks[i] = smftrack.New(uint16(id))
	}
	tracks = append(tracks, smftrack.New(2))
	tracks = append(tracks, smftrack.New(3))

	for i, track := range tracks {
		track.AddEvents(smftrack.Event{
			Message: meta.Tempo(120),
		})
		track.AddEvents(smftrack.Event{
			Message: meta.TimeSignature{4, 4},
		})
		switch i {
		case 0:
			tup := struct{ channel, program byte }{byte(i), 68}
			pc := *(*channel.ProgramChange)(unsafe.Pointer(&tup))
			track.AddEvents(smftrack.Event{
				Message: pc,
			})
		case 1, 3:
			tup := struct{ channel, program byte }{byte(i), 57}
			pc := *(*channel.ProgramChange)(unsafe.Pointer(&tup))
			track.AddEvents(smftrack.Event{
				Message: pc,
			})

		case 2:
			tup := struct{ channel, program byte }{byte(i), 60}
			pc := *(*channel.ProgramChange)(unsafe.Pointer(&tup))
			track.AddEvents(smftrack.Event{
				Message: pc,
			})
		}

		cctup := struct{ channel, program, value byte }{byte(i), 121, 0}
		cc := *(*channel.ControlChange)(unsafe.Pointer(&cctup))
		track.AddEvents(smftrack.Event{
			Message: cc,
		})

		cctup = struct{ channel, program, value byte }{byte(i), 7, 100}
		cc = *(*channel.ControlChange)(unsafe.Pointer(&cctup))
		track.AddEvents(smftrack.Event{
			Message: cc,
		})

		cctup = struct{ channel, program, value byte }{byte(i), 10, 64}
		cc = *(*channel.ControlChange)(unsafe.Pointer(&cctup))
		track.AddEvents(smftrack.Event{
			Message: cc,
		})

		cctup = struct{ channel, program, value byte }{byte(i), 91, 0}
		cc = *(*channel.ControlChange)(unsafe.Pointer(&cctup))
		track.AddEvents(smftrack.Event{
			Message: cc,
		})

		cctup = struct{ channel, program, value byte }{byte(i), 93, 0}
		cc = *(*channel.ControlChange)(unsafe.Pointer(&cctup))
		track.AddEvents(smftrack.Event{
			Message: cc,
		})
	}

	var tick uint64 = 960
	for _, in := range p.in {
		log.Printf("Decoding %v %v", in.key, in.duration)

		if in.key != 255 {
			tracks[0].AddEvents(
				smftrack.Event{
					AbsTicks: tick,
					Message:  channels[m[in.channel]].NoteOn(in.key, in.velocity),
				},

				smftrack.Event{
					AbsTicks: tick + uint64(in.duration),
					Message:  channels[m[in.channel]].NoteOff(in.key),
				},
			)
		}
		tick += uint64(in.duration)
	}

	for _, out := range p.out {
		if out.key != 255 {
			tracks[1].AddEvents(
				smftrack.Event{
					AbsTicks: tick,
					Message:  channels[m[out.channel]].NoteOn(out.key, 110),
				},

				smftrack.Event{
					AbsTicks: tick + uint64(out.duration),
					Message:  channels[m[out.channel]].NoteOff(out.key),
				},
			)
			tracks[2].AddEvents(
				smftrack.Event{
					AbsTicks: tick,
					Message:  channels[m[out.channel]].NoteOn(out.key, 106),
				},

				smftrack.Event{
					AbsTicks: tick + uint64(out.duration),
					Message:  channels[m[out.channel]].NoteOff(out.key),
				},
			)
			tracks[3].AddEvents(
				smftrack.Event{
					AbsTicks: tick,
					Message:  channels[m[out.channel]].NoteOn(out.key, 110),
				},

				smftrack.Event{
					AbsTicks: tick + uint64(out.duration),
					Message:  channels[m[out.channel]].NoteOff(out.key),
				},
			)
		}
		tick += uint64(out.duration)
	}

	f, _ := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	w := smfwriter.New(f, smfwriter.NumTracks(4), smfwriter.TimeFormat(tpq))
	for _, track := range tracks {
		track.WriteTo(w)
	}
	f.Close()

	// cb := func(msg smftrack.Event) {
	// 	switch m := msg.Message.(type) {
	// 	case channel.NoteOn:
	// 		log.Printf("Time: %v PLAY %v %v %v", msg.AbsTicks, m.Channel(), m.Key(), m.Velocity())
	// 	case channel.NoteOff:
	// 		log.Printf("TIme: %v STOP %v %v 0", msg.AbsTicks, m.Channel(), m.Key())
	// 	case meta.Tempo:
	// 		log.Printf("BPM: %v", m.BPM())
	// 	case meta.TimeSignature:
	// 		log.Printf("TS: %v/%v", m.Numerator, m.Denominator)
	// 	case channel.ProgramChange:
	// 		log.Printf("Time: %v PROGRAM CHANGE %v %v", msg.AbsTicks, m.Channel(), m.Program())
	// 	case channel.ControlChange:
	// 		log.Printf("Control Change %v - %v %v", m.Channel(), m.Controller(), m.Value())
	// 	default:
	// 		log.Printf("%T", m)
	// 	}
	// }
	// tracks[0].EachEvent(cb)
	// tracks[1].EachEvent(cb)
	// tracks[2].EachEvent(cb)
	// tracks[3].EachEvent(cb)
}

func parseStatus(b byte) (messageType, messageChannel byte) {
	messageType = (b & 0xF0) >> 4
	messageChannel = b & 0x0F
	return
}

func parseData(b byte) byte {
	return b & 0x7f
}
