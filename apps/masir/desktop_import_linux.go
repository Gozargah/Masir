package main

func stopOfflineService() error  { return run("systemctl", "stop", "masird.service") }
func startOfflineService() error { return run("systemctl", "start", "masird.service") }
