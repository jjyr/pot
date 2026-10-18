//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

int potNotificationAuthorization(void) {
    @autoreleasepool {
        if (![NSBundle mainBundle].bundleIdentifier) return -1;
        NSCondition *condition = [[NSCondition alloc] init];
        __block NSInteger status = -1;
        __block BOOL finished = NO;
        [[UNUserNotificationCenter currentNotificationCenter] getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
            [condition lock];
            status = settings.authorizationStatus;
            finished = YES;
            [condition signal];
            [condition unlock];
        }];
        [condition lock];
        NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:15];
        while (!finished) {
            if (![condition waitUntilDate:deadline]) break;
        }
        int result = finished ? (int)status : -1;
        [condition unlock];
        // The copied completion block retains condition if this read timed out.
        [condition release];
        return result;
    }
}
