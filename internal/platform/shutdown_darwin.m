//go:build darwin

#import <AppKit/AppKit.h>

extern void tacetSystemEnding(void);

static id tacetObserver = nil;

// NSWorkspaceWillPowerOffNotification is posted when the user logs out, restarts or shuts down.
void tacet_observe_power_off(void) {
	if (tacetObserver != nil) {
		return;
	}
	tacetObserver = [[[[NSWorkspace sharedWorkspace] notificationCenter]
		addObserverForName:NSWorkspaceWillPowerOffNotification object:nil queue:nil
		usingBlock:^(NSNotification *n) { tacetSystemEnding(); }] retain];
}

// Only for tests: what the system posts before it powers off.
void tacet_post_power_off(void) {
	[[[NSWorkspace sharedWorkspace] notificationCenter] postNotificationName:NSWorkspaceWillPowerOffNotification object:nil];
}
