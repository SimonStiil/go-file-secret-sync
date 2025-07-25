properties([disableConcurrentBuilds(), buildDiscarder(logRotator(artifactDaysToKeepStr: '5', artifactNumToKeepStr: '5', daysToKeepStr: '5', numToKeepStr: '5'))])

@Library('pipeline-library')
import dk.stiil.pipeline.Constants

podTemplate(yaml: '''
    apiVersion: v1
    kind: Pod
    spec:
      containers:
      - name: kaniko
        image: gcr.io/kaniko-project/executor:v1.23.2-debug
        command:
        - sleep
        args: 
        - 99d
        volumeMounts:
        - name: kaniko-secret
          mountPath: /kaniko/.docker
      - name: manifest-tool
        image: mplatform/manifest-tool:alpine-v2.2.0
        command:
        - sleep
        args: 
        - 99d
        volumeMounts:
        - name: kaniko-secret
          mountPath: /root/.docker
      - name: golang
        image: golang:1.24.2-alpine3.20
        command:
        - sleep
        args: 
        - 99d
        env:
        - name: HOST_NAME
          valueFrom:
            fieldRef:
              apiVersion: v1
              fieldPath: spec.nodeName
        volumeMounts:
        - name: "golang-cache"
          mountPath: "/root/.cache/"
        - name: "golang-prgs"
          mountPath: "/go/pkg/"
      restartPolicy: Never
      volumes:
      - name: kaniko-secret
        secret:
          secretName: github-dockercred
          items:
          - key: .dockerconfigjson
            path: config.json
      - name: "golang-cache"
        persistentVolumeClaim:
          claimName: "golang-cache"
      - name: "golang-prgs"
        persistentVolumeClaim:
          claimName: "golang-prgs"
''') {
  node(POD_LABEL) {
    TreeMap scmData
    String gitCommitMessage
    Map properties
    stage('checkout SCM') {  
      scmData = checkout scm
      gitCommitMessage = sh(returnStdout: true, script: "git log --format=%B -n 1 ${scmData.GIT_COMMIT}").trim()
      gitMap = scmGetOrgRepo scmData.GIT_URL
      githubWebhookManager gitMap: gitMap, webhookTokenId: 'jenkins-webhook-repo-cleanup'
      properties = readProperties file: 'package.env'
    }
    container('golang') {
      stage('Get CA Certs') {
        currentBuild.description = sh(returnStdout: true, script: 'echo $HOST_NAME').trim()
        sh '''
          apk --update add ca-certificates
          cp /etc/ssl/certs/ca-certificates.crt .
        '''
      }
      stage('UnitTests') {
        withEnv(['CGO_ENABLED=0']) {
          sh '''
            go test . -v
          '''
        }
      }
      stage('Build Application AMD64') {
        withEnv(['CGO_ENABLED=0', 'GOOS=linux', 'GOARCH=amd64', "PACKAGE_CONTAINER_APPLICATION=${properties.PACKAGE_CONTAINER_APPLICATION}"]) {
          sh '''
            go build -ldflags="-w -s" -o $PACKAGE_CONTAINER_APPLICATION-amd64 .
          '''
        }
      }
      stage('Build Application ARM64') {
        withEnv(['CGO_ENABLED=0', 'GOOS=linux', 'GOARCH=arm64', "PACKAGE_CONTAINER_APPLICATION=${properties.PACKAGE_CONTAINER_APPLICATION}"]) {
          sh '''
            go build -ldflags="-w -s" -o $PACKAGE_CONTAINER_APPLICATION-arm64 .
          '''
        }
      }
    }
    if ( !gitCommitMessage.startsWith("renovate/") || ! gitCommitMessage.startsWith("WIP") ) {
      container('golang') {
        stage('Generate Dockerfile AMD64') {
          sh '''
            ./dockerfilegen.sh amd64
          '''
        }
      }
      container('kaniko') {
        stage('Build Docker Image AMD64') {
          withEnv(["GIT_COMMIT=${scmData.GIT_COMMIT}", "PACKAGE_NAME=${properties.PACKAGE_NAME}", "PACKAGE_DESTINATION=${properties.PACKAGE_DESTINATION}", "PACKAGE_CONTAINER_SOURCE=${properties.PACKAGE_CONTAINER_SOURCE}", "GIT_BRANCH=${BRANCH_NAME}"]) {
            sh '''
                /kaniko/executor --force --context `pwd` --log-format text --custom-platform=linux/amd64 --destination $PACKAGE_DESTINATION/$PACKAGE_NAME:$BRANCH_NAME-amd64 --label org.opencontainers.image.description="Build based on $PACKAGE_CONTAINER_SOURCE/commit/$GIT_COMMIT" --label org.opencontainers.image.revision=$GIT_COMMIT --label org.opencontainers.image.version=$GIT_BRANCH
              '''
          }
        }
      }
      container('golang') {
        stage('Generate Dockerfile ARM64') {
          sh '''
            ./dockerfilegen.sh arm64
          '''
        }
      }
      container('kaniko') {
        stage('Build Docker Image ARM64') {
          withEnv(["GIT_COMMIT=${scmData.GIT_COMMIT}", "PACKAGE_NAME=${properties.PACKAGE_NAME}", "PACKAGE_DESTINATION=${properties.PACKAGE_DESTINATION}", "PACKAGE_CONTAINER_SOURCE=${properties.PACKAGE_CONTAINER_SOURCE}", "GIT_BRANCH=${BRANCH_NAME}"]) {
            sh '''
                /kaniko/executor --force --context `pwd` --log-format text --custom-platform=linux/arm64 --destination $PACKAGE_DESTINATION/$PACKAGE_NAME:$BRANCH_NAME-arm64 --label org.opencontainers.image.description="Build based on $PACKAGE_CONTAINER_SOURCE/commit/$GIT_COMMIT" --label org.opencontainers.image.revision=$GIT_COMMIT --label org.opencontainers.image.version=$GIT_BRANCH
              '''
          }
        }
      }
      container('manifest-tool') {
        stage('Build combined manifest') {
          withEnv(["GIT_COMMIT=${scmData.GIT_COMMIT}", "PACKAGE_NAME=${properties.PACKAGE_NAME}", "PACKAGE_DESTINATION=${properties.PACKAGE_DESTINATION}", "PACKAGE_CONTAINER_SOURCE=${properties.PACKAGE_CONTAINER_SOURCE}", "GIT_BRANCH=${BRANCH_NAME}"]) {
            if (isMainBranch()){
              sh 'manifest-tool push from-args --platforms linux/amd64,linux/arm64 --template $PACKAGE_DESTINATION/$PACKAGE_NAME:$BRANCH_NAME-ARCH --tags latest --target $PACKAGE_DESTINATION/$PACKAGE_NAME:$BRANCH_NAME'
            } else {
              sh 'manifest-tool push from-args --platforms linux/amd64,linux/arm64 --template $PACKAGE_DESTINATION/$PACKAGE_NAME:$BRANCH_NAME-ARCH --target $PACKAGE_DESTINATION/$PACKAGE_NAME:$BRANCH_NAME'
            }
          }
        }
      }
    }
    if (env.CHANGE_ID) {
      if (pullRequest.createdBy.equals("renovate[bot]")){
        if (pullRequest.mergeable) {
          stage('Approve and Merge PR') {
            pullRequest.merge(commitTitle: pullRequest.title, commitMessage: pullRequest.body, mergeMethod: 'squash')
          }
        }
      } else {
        echo "'PR Created by \""+ pullRequest.createdBy + "\""
      }
    }
  }
}