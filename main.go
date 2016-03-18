package main

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"bosun.org/metadata"
	"bosun.org/opentsdb"
	"bosun.org/util"
	"github.com/kisielk/sqlstruct"
	_ "github.com/mattn/go-sqlite3"
)

//CBProgramData Path to the ProgData directory for Cloudberry, typically C:\ProgramData\CloudBerry Backup Enterprise Edition
var CBProgramData = "C:\\ProgramData\\CloudBerry Backup Enterprise Edition"

var (
	sqlLiteDB           string
	cbbPlansBackups     []cbbBasePlan
	cbbPlansConsistency []cbbBasePlan
)

func main() {
	var numBackupJobs int

	err := filepath.Walk(filepath.Join(CBProgramData), processCBBFile)
	if err != nil {
		fmt.Println(err)
	}

	/*
		fmt.Println(fmt.Sprintf("Found %v CBB backup plans:", len(cbbPlansBackups)))
		for _, x := range cbbPlansBackups {
			fmt.Println(" ", x.Name)
		}
		fmt.Println(fmt.Sprintf("Found %v CBB consistency checks:", len(cbbPlansConsistency)))
		for _, x := range cbbPlansConsistency {
			fmt.Println(" ", x.Name)
		}
		fmt.Println("Found CBB database:")
		fmt.Println(" ", sqlLiteDB)
	*/
	if len(cbbPlansBackups) == 0 {
		panic("Did not locate any backup plans")
	}

	if sqlLiteDB == "" {
		panic("Did not locate Cloudberry database (cbbackup.db)")
	}

	db, err := sql.Open("sqlite3", sqlLiteDB)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	marshalToStdOut(metadata.Metasend{
		Metric: "cloudberry.job.files",
		Tags:   nil,
		Name:   "desc",
		Value:  "The operation taken on the file during the last job run. -1 = purged, 1 = backed up. Filenames are sanitised as such: Letters, numbers, periods and hyphens are unchanged. Slahes are converted to a pipe, spaces are converted to underscores. All other characters are stripped.",
	})

	for _, x := range cbbPlansBackups {
		var cbbSessionHistory cbbSessionHistoryRow
		var cbbHistory cbbHistoryRow
		var sqlStatement string

		sqlStatement = fmt.Sprintf(`SELECT %s FROM session_history WHERE plan_id = '%s' ORDER BY date_start_utc DESC LIMIT 0,1`, sqlstruct.Columns(cbbSessionHistory), x.ID)
		//fmt.Println(sqlStatement)
		rows, err := db.Query(sqlStatement, sqlstruct.Columns(cbbSessionHistoryRow{}))
		for rows.Next() {
			//fmt.Println(x.Name, ":")
			numBackupJobs++
			err = sqlstruct.Scan(&cbbSessionHistory, rows)
			if err != nil {
				fmt.Println(err)
			} else {
				timeTaken := time.Duration(cbbSessionHistory.Duration) * time.Second
				timeStarted, _ := cbbTimeToTime(cbbSessionHistory.DateStartUtc)
				//timeFinished := timeStarted.Add(timeTaken)
				//fmt.Println(fmt.Sprintf("  Last status:  %v\n  Uploaded:  %v\n  Size:      %vMB\n  Duration:  %v\n  Started:   %v\n  Finished:  %v", cbbJobStatuses[cbbSessionHistory.Result], cbbSessionHistory.UploadedCount, cbbSessionHistory.UploadedSize/1024/1024, timeTaken.String(), timeStarted, timeFinished))

				bosunDataPoint("cloudberry.job.status", cbbSessionHistory.Result, opentsdb.TagSet{"job": x.Name}, metadata.Gauge, metadata.Count, "The last reported status of the last job run.")
				bosunDataPoint("cloudberry.job.files_uploaded", cbbSessionHistory.UploadedCount, opentsdb.TagSet{"job": x.Name}, metadata.Gauge, metadata.Count, "The number of files uploaded in the last job run.")
				bosunDataPoint("cloudberry.job.job_duration", timeTaken.Seconds(), opentsdb.TagSet{"job": x.Name}, metadata.Gauge, metadata.Second, "The last reported duration of the job.")
				bosunDataPoint("cloudberry.job.time_since_last_start", time.Since(timeStarted).Seconds(), opentsdb.TagSet{"job": x.Name}, metadata.Gauge, metadata.Second, "Time since the job last started.")
				bosunDataPoint("cloudberry.job.size", cbbSessionHistory.UploadedSize, opentsdb.TagSet{"job": x.Name, "type": "uploaded"}, metadata.Gauge, metadata.Bytes, "The size reported by the last run of the job. Total size is the backup source that was scanned. Uploaded is the size of the data that was actually backed up.")
				bosunDataPoint("cloudberry.job.size", cbbSessionHistory.TotalSize, opentsdb.TagSet{"job": x.Name, "type": "total"}, metadata.Gauge, metadata.Bytes, "")

				sqlStatement = fmt.Sprintf(`SELECT %s FROM history WHERE plan_id = '%s' AND session_id = %v ORDER BY date_finished_utc ASC`, sqlstruct.Columns(cbbHistory), cbbSessionHistory.PlanID, cbbSessionHistory.ID)
				rows, err := db.Query(sqlStatement, sqlstruct.Columns(cbbHistoryRow{}))
				//fmt.Println("  Files:")
				for rows.Next() {
					err = sqlstruct.Scan(&cbbHistory, rows)
					if err != nil {
						fmt.Println(err)
					} else {
						opToSend := cbbHistory.Operation
						if opToSend == 0 {
							opToSend = -1
						}

						_, fileName := filepath.Split(cbbHistory.LocalPath)
						bosunDataPoint("cloudberry.job.files", opToSend, opentsdb.TagSet{"job": x.Name, "file": fileName}, metadata.Gauge, metadata.Count, "")
						//fmt.Println(fmt.Sprintf("    %v  %v", cbbHistoryOperations[cbbHistory.Operation], cbbHistory.LocalPath))
					}
				}

			}
		}
	}

	bosunDataPoint("cloudberry.jobs.count", numBackupJobs, opentsdb.TagSet{}, metadata.Count, metadata.Counter, "Number of backup jobs registered.")

}

func processCBBFile(path string, f os.FileInfo, ferr error) error {
	_, filename := filepath.Split(path)
	filename = strings.ToLower(filename)

	if filename == "cbbackup.db" {
		sqlLiteDB = path
		return nil
	}

	if filepath.Ext(filename) == ".cbb" {
		xBytes, xErr := ioutil.ReadFile(path)
		if xErr != nil {
			return xErr
		}

		var x cbbBasePlan
		xml.Unmarshal(xBytes, &x)
		if strings.Index(x.Name, "Consistency") == 0 {
			cbbPlansConsistency = append(cbbPlansConsistency, x)
		} else {
			cbbPlansBackups = append(cbbPlansBackups, x)
		}
		return nil
	}

	return nil
}

func escapeTagContent(v string) string {
	invalidChars := regexp.MustCompile("[^\\w.-]+")
	v = strings.Replace(v, " ", "_", -1)
	v = strings.Replace(v, "\\", "|", -1)
	v = strings.Replace(v, "/", "|", -1)
	v = invalidChars.ReplaceAllLiteralString(v, "")

	return v
}

func bosunDataPoint(name string, value interface{}, t opentsdb.TagSet, rate metadata.RateType, unit metadata.Unit, desc string) {
	if host, present := t["host"]; !present {
		t["host"] = util.Hostname
	} else if host == "" {
		delete(t, "host")
	}

	for k, v := range t {
		t[k] = escapeTagContent(v)
	}

	ts := time.Now().Unix()

	if rate != "" {
		marshalToStdOut(metadata.Metasend{
			Metric: name,
			Name:   "rate",
			Value:  rate,
		})
	}

	if unit != "" {
		marshalToStdOut(metadata.Metasend{
			Metric: name,
			Name:   "unit",
			Value:  unit,
		})
	}

	if desc != "" {
		marshalToStdOut(metadata.Metasend{
			Metric: name,
			Tags:   t,
			Name:   "desc",
			Value:  desc,
		})
	}

	marshalToStdOut(opentsdb.DataPoint{
		Metric:    name,
		Timestamp: ts,
		Value:     value,
		Tags:      t,
	})

}

func marshalToStdOut(v interface{}) error {
	bytes, err := json.Marshal(v)

	if err != nil {
		return err
	}

	fmt.Println(string(bytes))
	return nil
}
