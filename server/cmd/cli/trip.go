package main

import (
	"flag"
)

func cmdAddToTrip(c client, apiURL string, args []string) {
	fs := flag.NewFlagSet("add-to-trip", flag.ExitOnError)
	entry := fs.String("entry", "", "entry ID（必填）")
	trip := fs.String("trip", "", "trip ID（留空則新建）")
	title := fs.String("title", "", "新建 trip 時的行程名")
	_ = fs.Parse(args)
	if *entry == "" {
		fatal("add-to-trip 需要 -entry")
	}
	tripID, channelID, err := c.addToTrip(*entry, *trip, *title)
	if err != nil {
		fatal("add-to-trip: %v", err)
	}
	// DB 模式需手動 notify；HTTP 模式 server 端已自動廣播
	if channelID != "" {
		notifyChannel(channelID, apiURL)
	}
	output(map[string]string{"entryID": *entry, "tripID": tripID})
}

func cmdListTrips(c client, args []string) {
	fs := flag.NewFlagSet("list-trips", flag.ExitOnError)
	channel := fs.String("channel", "", "頻道 ID（必填）")
	_ = fs.Parse(args)
	if *channel == "" {
		fatal("list-trips 需要 -channel")
	}
	res, err := c.listTrips(*channel)
	if err != nil {
		fatal("list-trips: %v", err)
	}
	output(res)
}

func cmdTripEntries(c client, args []string) {
	fs := flag.NewFlagSet("trip-entries", flag.ExitOnError)
	channel := fs.String("channel", "", "頻道 ID（必填）")
	trip := fs.String("trip", "", "trip ID（必填）")
	_ = fs.Parse(args)
	if *channel == "" || *trip == "" {
		fatal("trip-entries 需要 -channel 與 -trip")
	}
	res, err := c.tripEntries(*channel, *trip)
	if err != nil {
		fatal("trip-entries: %v", err)
	}
	output(res)
}

func cmdCandidates(c client, args []string) {
	fs := flag.NewFlagSet("candidates", flag.ExitOnError)
	channel := fs.String("channel", "", "頻道 ID（必填）")
	start := fs.String("start", "", "開始時間（必填）")
	end := fs.String("end", "", "結束時間")
	_ = fs.Parse(args)
	if *channel == "" || *start == "" {
		fatal("candidates 需要 -channel 與 -start")
	}
	res, err := c.candidates(*channel, *start, *end)
	if err != nil {
		fatal("candidates: %v", err)
	}
	output(res)
}

func cmdDeleteTrip(c client, args []string) {
	fs := flag.NewFlagSet("delete-trip", flag.ExitOnError)
	trip := fs.String("trip", "", "trip ID（必填）")
	_ = fs.Parse(args)
	if *trip == "" {
		fatal("delete-trip 需要 -trip")
	}
	if err := c.deleteTrip(*trip); err != nil {
		fatal("delete-trip: %v", err)
	}
	output(map[string]string{"deleted": *trip})
}
