package devices

import (
	"context"
	"math"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/gpio/gpioreg"
)

var contextDecl, cancelFunc = context.WithCancel(context.Background())

// Context - The global context object
var Context = contextDecl

// CancelFunc - Call this to shutdown the app
var CancelFunc = cancelFunc

// OutputControl is a basic struct to handle heating outputs with a duty cyclke
type OutputControl struct {
	gorm.Model
	HeatOutput *OutPin
	CoolOutput *OutPin
	DutyCycle  int64 `gorm:"-"`
	CycleTime  int64 `gorm:"-"`
}

// OutPin represents a stored output pin with a friendly name
type OutPin struct {
	gorm.Model

	Identifier   string
	FriendlyName string
	PinIO        gpio.PinIO `gorm:"-"`
	onTime       *time.Time
	offTime      *time.Time
}

func (op *OutPin) off() bool {
	if op == nil {
		return false
	}

	if op.PinIO == nil {
		log.Warn().Msgf("Resetting off %v", op.Identifier)
		op.reset()
	}

	if op.PinIO == nil {
		log.Warn().Msgf("Cannot turn off %v", op.Identifier)
		return false
	}

	if op.offTime != nil && op.PinIO.Read() == gpio.Low {
		return false
	}

	if err := op.PinIO.Out(gpio.Low); err != nil {
		log.Fatal().Err(err).Msgf("Failed to set %v to Low (off)", op.FriendlyName)
	}
	curTime := time.Now()
	op.offTime = &curTime
	op.onTime = nil
	return true
}

func (op *OutPin) on() bool {
	if op == nil {
		return false
	}

	if op.PinIO == nil {
		log.Warn().Msgf("Rsetting on %v", op.Identifier)
		op.reset()
	}

	if op.PinIO == nil {
		log.Warn().Msgf("Cannot turn on %v", op.Identifier)
		return false
	}

	if op.onTime != nil && op.PinIO.Read() == gpio.High {
		return false
	}

	if err := op.PinIO.Out(gpio.High); err != nil {
		log.Fatal().Err(err).Msgf("Failed to set %v to High (on)", op.FriendlyName)
	}

	if op.PinIO.Read() != gpio.High {
		log.Warn().Msg("Failed to turn pin on! resetting and trying again")
		if err := op.PinIO.Out(gpio.High); err != nil {
			log.Fatal().Err(err).Msgf("Failed to set %v to High (on)", op.FriendlyName)
		}
		if op.PinIO.Read() != gpio.High {
			log.Warn().Msg("Failed to turn pin on!")
		}
	}
	curTime := time.Now()
	op.offTime = nil
	op.onTime = &curTime
	return true
}

func (op *OutPin) reset() {
	if op.Identifier == "" {
		if op != nil {
			op.off()
		}
		return
	}

	if op.PinIO == nil {
		op.PinIO = gpioreg.ByName(op.Identifier)
		if op.PinIO == nil {
			log.Fatal().Msgf("No Pin for %v!\n", op.Identifier)
		}
	}
	log.Warn().Msgf("Reset %v", op.Identifier)
	op.off()
}

func (op *OutPin) update(identifier string) {
	if identifier == "" {
		op.reset()
	} else if op.Identifier != identifier {
		log.Warn().Msgf("Updating identifier %v", identifier)
		op.reset()
		op.PinIO = nil
		op.Identifier = identifier
		op.reset()
	}
}

// Reset - Reset the output pins
func (o *OutputControl) Reset() {
	if o == nil {
		return
	}
	if o.HeatOutput != nil {
		o.HeatOutput.reset()
	}
	if o.CoolOutput != nil {
		o.CoolOutput.reset()
	}
}

// UpdateGpios - Update the heating and cooling outputs to their new pins
func (o *OutputControl) UpdateGpios(heatGpio string, coolGpio string) {
	// update the heating pin
	if o.HeatOutput == nil && heatGpio != "" {
		o.HeatOutput = &OutPin{Identifier: heatGpio, FriendlyName: "Heating"}
	} else if o.HeatOutput != nil {
		o.HeatOutput.update(heatGpio)
		if heatGpio == "" {
			o.HeatOutput = nil
		}
	}
	// update the cooling pin
	if o.CoolOutput == nil && coolGpio != "" {
		o.CoolOutput = &OutPin{Identifier: coolGpio, FriendlyName: "Cooling"}
	} else if o.CoolOutput != nil {
		o.CoolOutput.update(coolGpio)
		if coolGpio == "" {
			o.CoolOutput = nil
		}
	}
}

// CalculateOutput - Turn on and off the output pin for this output control depending on the duty cycle
func (o *OutputControl) CalculateOutput() {
	cycleSeconds := math.Abs(float64(o.CycleTime*o.DutyCycle) / 100)
	if o.DutyCycle == 0 {
		o.HeatOutput.off()
		o.CoolOutput.off()
	} else if o.DutyCycle == 100 {
		o.CoolOutput.off()
		if o.HeatOutput.on() {
			log.Info().Msgf("Turning on Heat Output (%v) for 100%% duty cycle", o.HeatOutput.FriendlyName)
		}
	} else if o.DutyCycle == -100 {
		o.HeatOutput.off()
		if o.CoolOutput.on() {
			log.Info().Msgf("Turning on Cool Output (%v) for -100%% duty cycle", o.CoolOutput.FriendlyName)
		}
	} else if o.DutyCycle > 0 {
		o.CoolOutput.off()
		if o.HeatOutput.onTime != nil {
			// it's on, do we need to turn it off?
			changedAt := time.Since(*o.HeatOutput.onTime)
			if changedAt.Seconds() > float64(cycleSeconds) {
				log.Info().Msgf("Heat output (%v) turning off after %v seconds", o.HeatOutput.FriendlyName, changedAt.Seconds())
				o.HeatOutput.off()
			}
		} else if o.HeatOutput.offTime != nil {
			// it's off, do we need to turn it on?
			changedAt := time.Since(*o.HeatOutput.offTime)
			offSeconds := float64(o.CycleTime) - cycleSeconds
			if changedAt.Seconds() >= offSeconds {
				log.Info().Msgf("Heat output (%v) turning on after %v seconds", o.HeatOutput.FriendlyName, changedAt.Seconds())
				o.HeatOutput.on()
			}
		} else {
			log.Info().Msgf("Heat output has no on or off time! %v", o.HeatOutput.Identifier)
			o.HeatOutput.off()
		}
	} else if o.DutyCycle < 0 {
		o.HeatOutput.off()

		if o.CoolOutput.onTime != nil {
			// it's on, do we need to turn it off?
			changedAt := time.Since(*o.CoolOutput.onTime)
			if changedAt.Seconds() > float64(cycleSeconds) {
				log.Info().Msgf("Cool output (%v) turning off after %v seconds\n", o.CoolOutput.FriendlyName, changedAt.Seconds())
				o.CoolOutput.off()
			}
		} else if o.CoolOutput.offTime != nil {
			// it's off, do we need to turn it on?
			changedAt := time.Since(*o.CoolOutput.offTime)
			offSeconds := float64(o.CycleTime) - cycleSeconds
			if changedAt.Seconds() >= offSeconds {
				log.Info().Msgf("Cool output (%v) turning on after %v seconds\n", o.CoolOutput.FriendlyName, changedAt.Seconds())
				o.CoolOutput.on()
			}
		}
	}
}

// RunControl -> Run the output controller for a heating output
func (o *OutputControl) RunControl(quit chan struct{}) {
	log.Info().Msgf("Starting output control")
	o.Reset()
	duration, err := time.ParseDuration("10ms")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse 10ms as a duration")
	}

	ticker := time.NewTicker(duration)

	for {
		select {
		case <-ticker.C:
			o.CalculateOutput()
		case <-quit:
			o.Reset()
			ticker.Stop()
			log.Info().Msg("Stop")
			return
		case <-Context.Done():
			o.Reset()
			log.Info().Msg("Done")
			return
		}
	}
}
