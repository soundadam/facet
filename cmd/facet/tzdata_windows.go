package main

// Windows has no zoneinfo database for time.LoadLocation to read, and
// profiles' time zones are checked against it.
import _ "time/tzdata"
