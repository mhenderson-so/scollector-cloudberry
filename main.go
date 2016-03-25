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

// CBProgramData is the path to the ProgData directory for Cloudberry, typically C:\ProgramData\CloudBerry Backup Enterprise Edition
var CBProgramData = "C:\\ProgramData\\CloudBerry Backup Enterprise Edition"

var (
	sqlLiteDB           string        //This will be the path to the CloudBerry SQL Lite database
	cbbPlansBackups     []cbbBasePlan //A collection of CloudBerry backup plans
	cbbPlansConsistency []cbbBasePlan //A collection of CloudBerry consistency check plans
)

//Metadata for the metrics that we are going to send to Bosun. Our metadata and counters are fairly simple, so we can just define them here and send them once,
//without having to send them again later.
var metaData = map[string]standardMetrics{
	"cloudberry.job.files":                 {metadata.Gauge, metadata.Count, "The operation taken on the file during the last job run. -1 = purged, 1 = backed up. Filenames are sanitised as such: Letters, numbers, periods and hyphens are unchanged. Slahes are converted to a hyphen, spaces are converted to underscores. All other characters are stripped."},
	"cloudberry.job.status":                {metadata.Gauge, metadata.Count, "The last reported status of the last job run."},
	"cloudberry.job.files_uploaded":        {metadata.Gauge, metadata.Count, "The number of files uploaded in the last job run."},
	"cloudberry.job.job_duration":          {metadata.Gauge, metadata.Second, "The last reported duration of the job."},
	"cloudberry.job.time_since_last_start": {metadata.Gauge, metadata.Second, "Time since the job last started."},
	"cloudberry.job.size_uploaded":         {metadata.Gauge, metadata.Bytes, "The size of the data that was uploaded as reported by the last run of the job."},
	"cloudberry.job.size_total":            {metadata.Gauge, metadata.Bytes, "The total size of the last backup job (i.e. not just what was uploaded)."},
	"cloudberry.job.count":                 {metadata.Gauge, metadata.Count, "Number of backup jobs registered."},
}

func main() {
	//Loop through all of the files that are in the CloudBerry ProgramData folder. We're ultimately looking for
	//*.cbb and cbbackup.db. *.cbb are the plan XML files, and cbbackup.db is the SQL Lite database
	err := filepath.Walk(filepath.Join(CBProgramData), processCBBFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	//If we don't have any backup plans, no point in continuing
	if len(cbbPlansBackups) == 0 {
		panic("Did not locate any backup plans")
	}

	//If we didn't locate an SQLLite database, no point in continuing
	if sqlLiteDB == "" {
		panic("Did not locate Cloudberry database (cbbackup.db)")
	}

	//Attempt to open the SQL Lite database. This should be safe, as SQL Lite doesn't lock anything unless you perform
	//a write command, which we have no intention of doing.
	db, err := sql.Open("sqlite3", sqlLiteDB)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	//We don't need to send the same metadata over and over and over again, so just send it once
	sendMetadata()

	//Log the number of jobs that we saw configured in CloudBerry (based on the number of XML, sorry .cbb, files we found)
	bosunDataPoint("cloudberry.jobs.count", len(cbbPlansBackups), opentsdb.TagSet{})

	//Process the backup plans. This is going to load the backup plan XML to get its metadata (name, etc). Then it's going to query the SQL Lite database
	//to get the history of the backup plan (files uploaded, time taken, etc). Once we have an individual historical run, we can query for more details
	//about that run, such as the actions taken during the run (backed up file, purged file, etc)
	for _, x := range cbbPlansBackups {
		var cbbSessionHistory cbbSessionHistoryRow //Holds the Session History (which is a record of each run of a backup job)
		var sqlStatement string

		//Get the most recent session history record for this backup plan
		sqlStatement = fmt.Sprintf(`SELECT %s FROM session_history WHERE plan_id = '%s' ORDER BY date_start_utc DESC LIMIT 0,1`, sqlstruct.Columns(cbbSessionHistory), x.ID)

		//Using the sqlstruct package here because the field names in the database are not valid GoLang field names. There are struct tags to map the GoLang name
		//to the SQL field name
		rows, err := db.Query(sqlStatement, sqlstruct.Columns(cbbSessionHistoryRow{}))
		for rows.Next() {
			err = sqlstruct.Scan(&cbbSessionHistory, rows)
			if err != nil {
				fmt.Fprintln(os.Stderr, err) //If we couldn't load the row into our object, throw this to stderr so that scollector can log the error
			} else {
				timeTaken := time.Duration(cbbSessionHistory.Duration) * time.Second //Create a GoLang representation of the amount of time the backup took
				timeStarted, _ := cbbTimeToTime(cbbSessionHistory.DateStartUtc)      //Get a GoLang representation of the time that the backup started at
				//timeFinished := timeStarted.Add(timeTaken)

				//Some stats that can be gleamed from the most recent history record. You check the the metadata at the top of this file if you want more details
				//about what is being sent here (look up the record with the same metric name)
				bosunDataPoint("cloudberry.job.status", cbbSessionHistory.Result, opentsdb.TagSet{"job": x.Name})
				bosunDataPoint("cloudberry.job.files_uploaded", cbbSessionHistory.UploadedCount, opentsdb.TagSet{"job": x.Name})
				bosunDataPoint("cloudberry.job.job_duration", timeTaken.Seconds(), opentsdb.TagSet{"job": x.Name})
				bosunDataPoint("cloudberry.job.time_since_last_start", time.Since(timeStarted).Seconds(), opentsdb.TagSet{"job": x.Name})
				bosunDataPoint("cloudberry.job.size_uploaded", cbbSessionHistory.UploadedSize, opentsdb.TagSet{"job": x.Name})
				bosunDataPoint("cloudberry.job.size_total", cbbSessionHistory.TotalSize, opentsdb.TagSet{"job": x.Name})

				//The following metrics are commented out for the time being, until we have nice regex matching rules in the config
				//Also, the make the output so big that scollector overruns the buffer scanner.

				/*
					var cbbHistory cbbHistoryRow               //Holds a History row (which is the list of files that were backed up during a session)

							//We have the basic details from the last run, now we can query the actual file operations that were undertaken during the run
							sqlStatement = fmt.Sprintf(`SELECT %s FROM history WHERE plan_id = '%s' AND session_id = %v ORDER BY date_finished_utc ASC`, sqlstruct.Columns(cbbHistory), cbbSessionHistory.PlanID, cbbSessionHistory.ID)
							rows, err := db.Query(sqlStatement, sqlstruct.Columns(cbbHistoryRow{}))
							for rows.Next() {
								err = sqlstruct.Scan(&cbbHistory, rows)
								if err != nil {
									fmt.Fprintln(os.Stderr, err) //If we couldn't load the row into our object, throw this to stderr so that scollector can log the error
								} else {
									//The Operation field in the database has a value of 0 for purged, but this doesn't really work very well in Bosun, so we change it to a -1
									//when logging it so that it clearly shows up as a purge in the stats
									opToSend := cbbHistory.Operation
									if opToSend == 0 {
										opToSend = -1
									}

									//We don't need to log the full path to the file in Bosun, so we're just going to log the file name
									_, fileName := filepath.Split(cbbHistory.LocalPath)
									bosunDataPoint("cloudberry.job.files", opToSend, opentsdb.TagSet{"job": x.Name, "file": fileName})
								}
							}
				*/
			}
		}
	}
}

//This processes the metadata supplied at the top of the file, and sends it to stdout, so that scollector
//can read it and send it off. Also means that we're only sending it once, not hundreds of times, which
//is nice.
func sendMetadata() {
	for thisMetricName, thisMetaData := range metaData {
		if thisMetaData.Rate != "" {
			marshalToStdOut(metadata.Metasend{
				Metric: thisMetricName,
				Name:   "rate",
				Value:  thisMetaData.Rate,
			})
		}

		if thisMetaData.Unit != "" {
			marshalToStdOut(metadata.Metasend{
				Metric: thisMetricName,
				Name:   "unit",
				Value:  thisMetaData.Unit,
			})
		}

		if thisMetaData.Desc != "" {
			marshalToStdOut(metadata.Metasend{
				Metric: thisMetricName,
				Name:   "desc",
				Value:  thisMetaData.Desc,
			})
		}
	}
}

//This is used when walking the directory structure of the CloudBerry ProgramData folder. It is looking for
//two specific things: .cbb files (which are actually XML files), and cbbackup.db, which is the CloudBerry
//SQL Lite database. Everything else we don't care about and ignore.
func processCBBFile(path string, f os.FileInfo, ferr error) error {
	_, filename := filepath.Split(path)
	filename = strings.ToLower(filename)

	if filename == "cbbackup.db" {
		sqlLiteDB = path
		return nil
	}

	//If we have found a CBB file, we're going to unmarshal the XML file into a GoLang object so that
	//we can read it later on when we're going through the jobs.
	if filepath.Ext(filename) == ".cbb" {
		xBytes, xErr := ioutil.ReadFile(path) //Read the file in
		if xErr != nil {
			return xErr //Can't read the file? Booo.
		}

		var x cbbBasePlan                              //Create a cbbBasePlan object to store the unmarshalled XML file
		xml.Unmarshal(xBytes, &x)                      //Attempt to unmarshall it
		if strings.Index(x.Name, "Consistency") == 0 { //Is this a consistency check plan? If it is, put it into the consistency object, not the job object
			cbbPlansConsistency = append(cbbPlansConsistency, x)
		} else { //Ok, put it into the backup object
			cbbPlansBackups = append(cbbPlansBackups, x)
		}
		return nil
	}
	return nil
}

//Take a metric, a value, and a tagset and output it to stdout so that scollector can receive it
//and send it to Bosun.
func bosunDataPoint(name string, value interface{}, t opentsdb.TagSet) {

	//Make sure the host is correct in the tagset, or if we explicitly don't want a hostname field, delete it.
	if host, present := t["host"]; !present {
		t["host"] = util.Hostname
	} else if host == "" {
		delete(t, "host")
	}

	//There's a bunch of data that comes into the tag set that contains invalid characters, so this should
	//strip them out. This is not a one-size fits all function, it's probably only suitable for dealing with
	//filenames, like we are here.
	for k, v := range t {
		t[k] = escapeTagContent(v)
	}

	ts := time.Now().Unix()

	//Send that metric to stdout, thanks.
	marshalToStdOut(opentsdb.DataPoint{
		Metric:    name,
		Timestamp: ts,
		Value:     value,
		Tags:      t,
	})

}

//Filenames have all sorts of stuff in them that is not valid as a Bosun tag value. We're removing everything but:
//A-Z, a-z, 0-8, ., -
func escapeTagContent(v string) string {
	invalidChars := regexp.MustCompile("[^\\w.-]+")
	v = strings.Replace(v, " ", "_", -1)
	v = strings.Replace(v, "\\", "-", -1)
	v = strings.Replace(v, "/", "-", -1)
	v = invalidChars.ReplaceAllLiteralString(v, "")
	return v
}

//Send the object to stdout in JSON form
func marshalToStdOut(v interface{}) error {
	bytes, err := json.Marshal(v)

	if err != nil {
		return err
	}

	fmt.Println(string(bytes))
	return nil
}
