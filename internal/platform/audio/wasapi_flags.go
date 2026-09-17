package audio

const (
	wasapiBufferFlagDataDiscontinuity uint32 = 0x1
	wasapiBufferFlagSilent            uint32 = 0x2
	wasapiBufferFlagTimestampError    uint32 = 0x4
)

type wasapiBufferFlags struct {
	Discontinuity  bool
	Silent         bool
	TimestampError bool
}

func decodeWASAPIBufferFlags(flags uint32) wasapiBufferFlags {
	return wasapiBufferFlags{
		Discontinuity:  flags&wasapiBufferFlagDataDiscontinuity != 0,
		Silent:         flags&wasapiBufferFlagSilent != 0,
		TimestampError: flags&wasapiBufferFlagTimestampError != 0,
	}
}
