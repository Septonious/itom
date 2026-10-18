#!/usr/bin/env python3
"""LAB ONLY RC7 directional flight cap, isolated from its faster growth policy."""
from pathlib import Path
import shutil
r=Path(__file__).resolve().parents[3];root=r/'archives/1.0.0-rc10/source/core/tunnel'
p=root/'delivery_control.go';s=p.read_text()
def sub(a,b):
 global s
 assert s.count(a)==1,(a,s.count(a));s=s.replace(a,b)
sub('deliveryMaxWindow     = 16 << 20','deliveryMaxWindow     = 16 << 20\n\tdeliveryClientMaxWindow = 12 << 20')
sub('initialWindow                       uint64','initialWindow                       uint64\n\tmaxWindow uint64')
sub('initialWindow: deliveryInitialWindow, ranges:', 'initialWindow: deliveryInitialWindow, maxWindow: deliveryMaxWindow, ranges:')
sub('func (d *deliveryController) init() {','''func (d *deliveryController) setRoleWindow(server bool) {
 d.init()
 if server { d.maxWindow = deliveryMaxWindow } else { d.maxWindow = deliveryClientMaxWindow }
 d.window = min(d.window, d.maxWindow)
}
func (d *deliveryController) init() {''')
sub('if d.wake == nil {','if d.maxWindow == 0 { d.maxWindow = deliveryMaxWindow }\n\tif d.wake == nil {')
sub('InitialWindow: d.initialWindow, Flight:', 'InitialWindow: d.initialWindow, MaxWindow: d.maxWindow, Flight:')
sub('d.bulk+size+n <= deliveryMaxWindow','d.bulk+size+n <= d.maxWindow')
sub('if d.bulk+bytes > deliveryMaxWindow {','if d.bulk+bytes > d.maxWindow {')
p.write_text(s)
p=root/'delivery_policy.go';s=p.read_text().replace('Window, Flight, ACKFlight, Delivered, InitialWindow uint64','Window, Flight, ACKFlight, Delivered, InitialWindow, MaxWindow uint64')
s=s.replace('window := f.Window','window := f.Window\n\tlimit := f.MaxWindow\n\tif limit == 0 { limit = deliveryMaxWindow }')
s=s.replace('if window >= deliveryMaxWindow {','if window >= limit {').replace('return min(uint64(deliveryMaxWindow), window+step)','return min(limit, window+step)');p.write_text(s)
p=root/'stripe.go';s=p.read_text();a='g.mu.Lock()\n\told := g.lanes[l.account.ID]';assert s.count(a)==1
p.write_text(s.replace(a,'g.mu.Lock()\n\tg.delivery.setRoleWindow(l.side)\n\told := g.lanes[l.account.ID]'))
shutil.copy(r/'archives/1.0.0-rc9/source/core/tunnel/rc7_asymmetric_window_test.go',root/'rc7_asymmetric_window_test.go')
