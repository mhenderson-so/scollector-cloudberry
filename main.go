package main

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kisielk/sqlstruct"
	_ "github.com/mattn/go-sqlite3"
)

//CBProgramData Path to the ProgData directory for Cloudberry, typically C:\ProgramData\CloudBerry Backup Enterprise Edition
var CBProgramData = "C:\\Temp\\CloudBerry Backup Enterprise Edition"

var (
	sqlLiteDB           string
	cbbPlansBackups     []cbbBasePlan
	cbbPlansConsistency []cbbBasePlan
)

func main() {
	err := filepath.Walk(filepath.Join(CBProgramData), processCBBFile)
	if err != nil {
		fmt.Println(err)
	}

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

	for _, x := range cbbPlansBackups {
		var cbbSessionHistory cbbSessionHistoryRow
		var cbbHistory cbbHistoryRow
		var sqlStatement string

		sqlStatement = fmt.Sprintf(`SELECT %s FROM session_history WHERE plan_id = '%s' ORDER BY date_start_utc DESC LIMIT 0,1`, sqlstruct.Columns(cbbSessionHistory), x.ID)
		//fmt.Println(sqlStatement)
		rows, err := db.Query(sqlStatement, sqlstruct.Columns(cbbSessionHistoryRow{}))
		for rows.Next() {
			fmt.Println(x.Name, ":")
			err = sqlstruct.Scan(&cbbSessionHistory, rows)
			if err != nil {
				fmt.Println(err)
			} else {
				timeTaken := time.Duration(cbbSessionHistory.Duration) * time.Second
				timeStarted, _ := cbbTimeToTime(cbbSessionHistory.DateStartUtc)
				timeFinished := timeStarted.Add(timeTaken)
				fmt.Println(fmt.Sprintf("  Last status:  %v\n  Uploaded:  %v\n  Size:      %vMB\n  Duration:  %v\n  Started:   %v\n  Finished:  %v", cbbJobStatuses[cbbSessionHistory.Result], cbbSessionHistory.UploadedCount, cbbSessionHistory.UploadedSize/1024/1024, timeTaken.String(), timeStarted, timeFinished))

				sqlStatement = fmt.Sprintf(`SELECT %s FROM history WHERE plan_id = '%s' AND date_finished_utc BETWEEN '%v' AND '%v' ORDER BY date_finished_utc ASC`, sqlstruct.Columns(cbbHistory), x.ID, timeToCbbTime(timeStarted), timeToCbbTime(timeFinished))
				rows, err := db.Query(sqlStatement, sqlstruct.Columns(cbbHistoryRow{}))
				fmt.Println("  Files:")
				for rows.Next() {
					err = sqlstruct.Scan(&cbbHistory, rows)
					if err != nil {
						fmt.Println(err)
					} else {
						fmt.Println(fmt.Sprintf("    %v  %v", cbbHistoryOperations[cbbHistory.Operation], cbbHistory.LocalPath))
					}
				}
			}
		}
	}
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

var cbbTimeFormat = "20060102150405"

func cbbTimeToTime(cbbTime string) (time.Time, error) {
	return time.Parse(cbbTimeFormat, cbbTime)
}

func timeToCbbTime(thisTime time.Time) string {
	return thisTime.Format(cbbTimeFormat)
}

var cbbJobStatuses = []string{
	"unknown",          //0
	"unknown",          //1
	"unknown",          //2
	"unknown",          //3
	"unknown",          //4
	"unknown",          //5
	"success",          //6
	"unknown",          //7
	"unknown",          //8
	"user interrupted", //9
}

var cbbHistoryOperations = []string{
	"purge",   //0
	"backup",  //1
	"unknown", //2
	"unknown", //3
	"unknown", //4
	"unknown", //5
	"unknown", //6
	"unknown", //7
	"unknown", //8
	"unknown", //9

}

type cbbHistoryRow struct {
	ID              int     `sql:"id"`
	DestinationID   int     `sql:"destination_id"`
	PlanID          string  `sql:"plan_id"`
	LocalPath       string  `sql:"local_path"`
	Operation       int     `sql:"operation"`
	Duration        int     `sql:"duration"`
	DateFinishedUtc string  `sql:"date_finished_utc"`
	DateModifiedUtc string  `sql:"date_modified_utc"`
	Size            float32 `sql:"size"`
	Message         string  `sql:"message"`
	SessionID       int     `sql:"session_id"`
	Attempts        int     `sql:"attempts"`
}

type cbbSessionHistoryRow struct {
	ID              int     `sql:"id"`
	DestinationID   int     `sql:"destination_id"`
	PlanID          string  `sql:"plan_id"`
	DateStartUtc    string  `sql:"date_start_utc"`
	Duration        int     `sql:"duration"`
	Result          int     `sql:"result"`
	UploadedCount   int     `sql:"uploaded_count"`
	UploadedSize    float32 `sql:"uploaded_size"`
	ScannedCount    int     `sql:"scanned_count"`
	ScannedSize     float32 `sql:"scanned_size"`
	PurgedCount     int     `sql:"purged_count"`
	TotalCount      int     `sql:"total_count"`
	TotalSize       float32 `sql:"total_size"`
	FailedCount     int     `sql:"failed_count"`
	ErrorMessage    string  `sql:"error_message"`
	ProcessorTime   int     `sql:"processor_time"`
	PeakMemoryUsage float32 `sql:"peak_memory_usage"`
}

type cbbBasePlan struct {
	ExcludeFodlerList                           string   `xml:"ExcludeFodlerList"`
	OnceDateSchedule                            string   `xml:"Schedule>OnceDate"`
	ConnectionID                                string   `xml:"ConnectionID"`
	Arguments                                   string   `xml:"Actions>Pre>Arguments"`
	Path                                        []string `xml:"Items>PlanItem>Path"`
	RetentionDelay                              string   `xml:"RetentionDelay"`
	DeleteIfDeletedLocallyAfter                 string   `xml:"DeleteIfDeletedLocallyAfter"`
	DailyRecurrence                             string   `xml:"ForceFullSchedule>DailyRecurrence"`
	OnceDate                                    string   `xml:"ForceFullSchedule>OnceDate"`
	EncryptionAlgorithm                         string   `xml:"EncryptionAlgorithm"`
	Xsi                                         string   `xml:"xsi,attr"`
	EncryptionKeySize                           string   `xml:"EncryptionKeySize"`
	DeleteCloudVersionIfDeletedLocally          string   `xml:"DeleteCloudVersionIfDeletedLocally"`
	DailyRecurrencePeriod                       string   `xml:"ForceFullSchedule>DailyRecurrencePeriod"`
	DayOfMonth                                  string   `xml:"ForceFullSchedule>DayOfMonth"`
	UseCompression                              string   `xml:"UseCompression"`
	SSEKMSKeyID                                 string   `xml:"SSEKMSKeyID"`
	RetentionDeleteLastVersion                  string   `xml:"RetentionDeleteLastVersion"`
	DayOfWeek                                   []string `xml:"ForceFullSchedule>WeekDays>DayOfWeek"`
	UseEncryption                               string   `xml:"UseEncryption"`
	FilterTypeBackupFilter                      string   `xml:"BackupFilter>FilterType"`
	SkipInUseFiles                              string   `xml:"SkipInUseFiles"`
	UseServerSideEncryption                     string   `xml:"UseServerSideEncryption"`
	OnlyOnFailure                               string   `xml:"Notification>OnlyOnFailure"`
	StopAfterTicks                              string   `xml:"ForceFullSchedule>StopAfterTicks"`
	EnabledSchedule                             string   `xml:"Schedule>Enabled"`
	WeekDays                                    []string `xml:"Schedule>WeekDays"`
	Filters                                     string   `xml:"CompressionFilter>Filters"`
	EncryptionPassword                          string   `xml:"EncryptionPassword"`
	Minutes                                     string   `xml:"ForceFullSchedule>Minutes"`
	ArgumentsPostActions                        string   `xml:"Actions>Post>Arguments"`
	HourSchedule                                string   `xml:"Schedule>Hour"`
	DayOfWeekSchedule                           string   `xml:"Schedule>DayOfWeek"`
	DayOfMonthSchedule                          string   `xml:"Schedule>DayOfMonth"`
	UseVSSFullMode                              string   `xml:"UseVSSFullMode"`
	Type                                        string   `xml:"type,attr"`
	SyncBeforeRun                               string   `xml:"SyncBeforeRun"`
	Seconds                                     string   `xml:"ForceFullSchedule>Seconds"`
	DailyTillMinutes                            string   `xml:"ForceFullSchedule>DailyTillMinutes"`
	SecondsSchedule                             string   `xml:"Schedule>Seconds"`
	ForceMissedSchedule                         string   `xml:"ForceMissedSchedule"`
	CommandLinePostActions                      string   `xml:"Actions>Post>CommandLine"`
	BackupOnlyAfterUTC                          string   `xml:"BackupOnlyAfterUTC"`
	ID                                          string   `xml:"ID"`
	DayOfWeekForceFullSchedule                  string   `xml:"ForceFullSchedule>DayOfWeek"`
	RepeatEverySchedule                         string   `xml:"Schedule>RepeatEvery"`
	SendNotificationWindowsEventLogNotification string   `xml:"WindowsEventLogNotification>SendNotification"`
	RetentionNumberOfVersions                   string   `xml:"RetentionNumberOfVersions"`
	MaxFileSize                                 string   `xml:"MaxFileSize"`
	Name                                        string   `xml:"Name"`
	Xsd                                         string   `xml:"xsd,attr"`
	SendNotification                            string   `xml:"Notification>SendNotification"`
	UseRRS                                      string   `xml:"UseRRS"`
	RecurType                                   string   `xml:"ForceFullSchedule>RecurType"`
	Hour                                        string   `xml:"ForceFullSchedule>Hour"`
	ExcludedItems                               string   `xml:"ExcludedItems"`
	ForceFullApplyDiffSizeCondition             string   `xml:"ForceFullApplyDiffSizeCondition"`
	RecurTypeSchedule                           string   `xml:"Schedule>RecurType"`
	DailyFromHourSchedule                       string   `xml:"Schedule>DailyFromHour"`
	MinutesSchedule                             string   `xml:"Schedule>Minutes"`
	EnabledPostActions                          string   `xml:"Actions>Post>Enabled"`
	IncludeSystemAndHidden                      string   `xml:"CompressionFilter>IncludeSystemAndHidden"`
	SavePlanInCloud                             string   `xml:"SavePlanInCloud"`
	GenerateReport                              string   `xml:"Notification>GenerateReport"`
	DailyFromMinutes                            string   `xml:"ForceFullSchedule>DailyFromMinutes"`
	WeekNumber                                  string   `xml:"ForceFullSchedule>WeekNumber"`
	BackupNTFSPermissions                       string   `xml:"BackupNTFSPermissions"`
	Enabled                                     string   `xml:"ForceFullSchedule>Enabled"`
	DailyTillHourSchedule                       string   `xml:"Schedule>DailyTillHour"`
	TerminateOnFailure                          string   `xml:"Actions>Pre>TerminateOnFailure"`
	RetentionUseDefaultSettings                 string   `xml:"RetentionUseDefaultSettings"`
	UseShareReadWriteModeOnError                string   `xml:"UseShareReadWriteModeOnError"`
	BackupEmptyFolders                          string   `xml:"BackupEmptyFolders"`
	UseDifferentialUpload                       string   `xml:"UseDifferentialUpload"`
	RepeatEvery                                 string   `xml:"ForceFullSchedule>RepeatEvery"`
	OnlyOnFailureWindowsEventLogNotification    string   `xml:"WindowsEventLogNotification>OnlyOnFailure"`
	IsArchive                                   string   `xml:"IsArchive"`
	IsSimple                                    string   `xml:"IsSimple"`
	EnabledPreActions                           string   `xml:"Actions>Pre>Enabled"`
	CommandLine                                 string   `xml:"Actions>Pre>CommandLine"`
	Timeout                                     string   `xml:"Actions>Pre>Timeout"`
	RunOnBackupFailure                          string   `xml:"Actions>Post>RunOnBackupFailure"`
	FilterType                                  string   `xml:"CompressionFilter>FilterType"`
	Subject                                     string   `xml:"Notification>Subject"`
	UseStandardIA                               string   `xml:"UseStandardIA"`
	DailyRecurrenceSchedule                     string   `xml:"Schedule>DailyRecurrence"`
	DailyRecurrencePeriodSchedule               string   `xml:"Schedule>DailyRecurrencePeriod"`
	IncludeSystemAndHiddenBackupFilter          string   `xml:"BackupFilter>IncludeSystemAndHidden"`
	AlwaysUseVSS                                string   `xml:"AlwaysUseVSS"`
	DailyFromMinutesSchedule                    string   `xml:"Schedule>DailyFromMinutes"`
	DailyTillMinutesSchedule                    string   `xml:"Schedule>DailyTillMinutes"`
	WeekNumberSchedule                          string   `xml:"Schedule>WeekNumber"`
	DeleteIfDeletedLocallyAfterInterval         string   `xml:"DeleteIfDeletedLocallyAfterInterval"`
	BackupOnlyModifiedDaysAgo                   string   `xml:"BackupOnlyModifiedDaysAgo"`
	SerializationSupportRetentionTime           string   `xml:"SerializationSupportRetentionTime"`
	DailyTillHour                               string   `xml:"ForceFullSchedule>DailyTillHour"`
	StopAfterTicksSchedule                      string   `xml:"Schedule>StopAfterTicks"`
	FiltersBackupFilter                         string   `xml:"BackupFilter>Filters"`
	UseFileNameEncryption                       string   `xml:"UseFileNameEncryption"`
	DailyFromHour                               string   `xml:"ForceFullSchedule>DailyFromHour"`
	ForceFullDiffSizeCondition                  string   `xml:"ForceFullDiffSizeCondition"`
	TimeoutPostActions                          string   `xml:"Actions>Post>Timeout"`
}
