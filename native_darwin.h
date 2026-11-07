#ifndef CUEVIA_NATIVE_DARWIN_H
#define CUEVIA_NATIVE_DARWIN_H

int cuevia_start_hotkey(void);
int cuevia_dispatch_next_event(void);
void cuevia_stop_hotkey(void);
int cuevia_screen_capture_allowed(void);

#endif
