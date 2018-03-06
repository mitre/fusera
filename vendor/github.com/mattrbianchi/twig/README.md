# Twig

An experimental, lightweight log package based off of Dave Cheney's post: https://dave.cheney.net/2015/11/05/lets-talk-about-logging

The goal of twig was to see if a wrapper around Go's own standard log package could be made that would promote Dave's suggestions around good logging practices.

On one hand, this package hides a lot of the log functions in the standard log package.

On the other, it adds a `Debug` level where developers could control whether messages logged with the `Debug` and `Debugf` functions actually make it to their intended destination or vanish into thin air for production environments through the use of `SetDebug`.

The only other level is `Info`, used through the `Info` and `Infof` functions. These functions will always write their messages to their Writers.

Because Twig is just a wrapper, one can customize it the same as with the standard log package.

