package features

import (
	// "context"
	"fmt"
	// "log"
	// powertrace "kube-scheduler/powertrace"
)

func TraceGoogleWorkload() {
	// ctx := context.Background()

	// traces, err := powertrace.DownloadAndParse(ctx, "powerdata_2019", "cella_pdu6.csv.gz")
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// fmt.Printf("Parsed %d records\n", len(traces))

	// // Save output to /data/powertrace.csv
	// if err := powertrace.SaveToCSV(traces, "data/powertrace.csv"); err != nil {
	// 	log.Fatal(err)
	// }

	fmt.Println("Saved to data/powertrace.csv")
}
