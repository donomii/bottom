[![Build Status](https://travis-ci.org/donomii/bottom.svg?branch=master)](https://travis-ci.org/donomii/bottom)
# Bottom
The opposite of top - show processes as they start and end

# What is happening to my system
Top shows you programs that are currently running, but I needed to see which programs were starting and stopping.  This can show problems like a program constantly crashing and restarting, or a bad program waking up in the background to do something.

# Use

    bottom
  
    bottom.exe
  
Run bottom, watch the screen.  Bottom will print out program names as they start and stop.

# Bugs
Bottom currently scans for new programs by requesting the process table over and over.  Programs that start and then quit very quickly can evade bottom.

If someone lets me know how to subscribe to notifications of a program starting and stopping, I'll add that in.

