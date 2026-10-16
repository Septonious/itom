package wire

// MergeProgressFrames coalesces unsent authenticated-state snapshots from the
// same logical owner. Sender and receiver frontiers advance independently:
// choosing solely by Frame.Offset can discard newer credit or receipt state.
// Never use this on sealed records or snapshots from different generations.
func MergeProgressFrames(a, b Frame) (Frame, bool) {
	if a.Type != Status || b.Type != Status || a.Stream != b.Stream {
		return Frame{}, false
	}
	ca, ea := DecodeControl(a)
	cb, eb := DecodeControl(b)
	if ea != nil || eb != nil {
		return Frame{}, false
	}
	x, y := ca.(Progress), cb.(Progress)
	if x.HasFinal && y.HasFinal && x.Final != y.Final {
		return Frame{}, false
	}
	x.Attempted = max(x.Attempted, y.Attempted)
	x.Receipt = max(x.Receipt, y.Receipt)
	x.MaxStream = max(x.MaxStream, y.MaxStream)
	x.MaxData = max(x.MaxData, y.MaxData)
	if y.HasFinal {
		x.HasFinal, x.Final = true, y.Final
	}
	f, err := EncodeControl(a.Stream, x)
	return f, err == nil
}
