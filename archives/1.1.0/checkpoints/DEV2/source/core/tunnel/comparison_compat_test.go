package tunnel
import "reflect"
// -1 means the historical production diagnostic did not expose this field.
// This helper reads optional logs only, never workload or acceptance state.
func comparisonDiagnosticInt(snapshot any, field string) int64 {
 v := reflect.ValueOf(snapshot).FieldByName(field)
 if !v.IsValid() { return -1 }
 if v.CanInt() { return v.Int() }
 if v.CanUint() { return int64(v.Uint()) }
 panic("unsupported comparison diagnostic")
}
